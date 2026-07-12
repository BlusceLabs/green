package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/BlusceLabs/green/internal/config"
	"github.com/BlusceLabs/green/internal/oauth"
	"github.com/BlusceLabs/green/internal/providers/providerio"
)

// oauthLoginForProfile resolves the user's OAuth login for a provider ONCE and
// returns both a TokenResolver that authenticates model calls with it and the
// credential-store key it bound to. It returns (nil, "") when no login exists —
// keeping API-key users free of any per-request store lookups, since the resolver
// is only attached when a login is present at construction time.
//
// The returned key is the single source of truth for "which login is this
// provider using": callers pass it to providers.Options.OAuthLoginKey so the
// Codex chatgpt-account-id header reads its account from the exact login that
// issued the bearer token, instead of doing a second, independent lookup that
// could select a different login (a backend-rejected mismatch).
//
// Candidate login names (profile name, then a catalog-ID fallback, both gated on
// the profile having no own configured credential) come from the shared
// ProviderProfile.OAuthLoginCandidates so the runtime resolver, the Codex account
// resolver, and the onboarding presence check never diverge.
func oauthLoginForProfile(profile config.ProviderProfile) (providerio.TokenResolver, string) {
	candidates := profile.OAuthLoginCandidates()
	if len(candidates) == 0 {
		return nil, ""
	}
	store, err := oauth.NewStore(oauth.StoreOptions{})
	if err != nil {
		return nil, ""
	}
	_, key, ok := oauth.FirstStored(store, candidates)
	if !ok {
		// No login under any candidate (or unreadable/invalid keys) → API-key
		// auth, no resolver.
		return nil, ""
	}
	manager, err := oauth.NewManager(oauth.ManagerOptions{
		Store:      store,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		// Refreshing a token the user logged into (possibly a preset provider like
		// xAI) re-resolves that provider's OAuth config, which needs the preset.
		AllowPresets: true,
	})
	if err != nil {
		return nil, ""
	}
	resolver := func(ctx context.Context, forceRefresh bool) (string, string, bool, error) {
		var token string
		var rerr error
		if forceRefresh {
			token, rerr = manager.Handle401(ctx, key)
		} else {
			token, rerr = manager.GetFresh(ctx, key)
		}
		if errors.Is(rerr, oauth.ErrNoToken) {
			// The login was removed since construction → fall back to the API key.
			return "", "", false, nil
		}
		if rerr != nil {
			return "", "", false, rerr
		}
		return "Authorization", "Bearer " + token, true, nil
	}
	return resolver, key
}

// githubCopilotLoginKey is the OAuth login the "github-copilot" provider consumes.
// The login identity is "github" (a GitHub device-flow OAuth login), not
// "github-copilot" — the Copilot profile only exists as a model-serving consumer.
const githubCopilotLoginKey = "github"

// githubCopilotResolver returns a TokenResolver that authenticates the
// github-copilot OpenAI-compatible endpoint. Each request exchanges the stored
// GitHub OAuth token for a short-lived Copilot access token (cached until
// expiry) and returns it as a bearer. When no GitHub login exists it returns
// (nil, "") so the provider falls back to any configured API key.
//
// Exchange: POST https://api.github.com/copilot_internal/v2/token with
// `Authorization: Bearer <github token>` and `Accept: application/json`; the
// response `{token, expires_at}` yields the Copilot bearer (expires_at is a
// Unix epoch seconds).
func githubCopilotResolver() (providerio.TokenResolver, string) {
	store, err := oauth.NewStore(oauth.StoreOptions{})
	if err != nil {
		return nil, ""
	}
	if _, _, ok := oauth.FirstStored(store, []string{githubCopilotLoginKey}); !ok {
		return nil, ""
	}
	manager, err := oauth.NewManager(oauth.ManagerOptions{
		Store:      store,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		// Refreshing the GitHub login re-resolves its preset (device flow).
		AllowPresets: true,
	})
	if err != nil {
		return nil, ""
	}
	var cachedToken string
	var cachedExpiry time.Time
	resolver := func(ctx context.Context, forceRefresh bool) (string, string, bool, error) {
		if cachedToken != "" && time.Now().Before(cachedExpiry) && !forceRefresh {
			return "Authorization", "Bearer " + cachedToken, true, nil
		}
		githubToken, rerr := manager.GetFresh(ctx, oauth.ProviderKey(githubCopilotLoginKey))
		if rerr != nil {
			return "", "", false, rerr
		}
		copilotToken, expiry, eerr := exchangeCopilotToken(ctx, githubToken)
		if eerr != nil {
			return "", "", false, eerr
		}
		cachedToken = copilotToken
		cachedExpiry = expiry
		return "Authorization", "Bearer " + copilotToken, true, nil
	}
	return resolver, githubCopilotLoginKey
}

// copilotTokenURL is the GitHub Copilot token-exchange endpoint. It is a var so
// tests can point it at a local mock server.
var copilotTokenURL = "https://api.github.com/copilot_internal/v2/token"

// exchangeCopilotToken trades a GitHub OAuth bearer for a Copilot access token.
func exchangeCopilotToken(ctx context.Context, githubToken string) (string, time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, copilotTokenURL, nil)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("copilot token exchange: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+githubToken)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("copilot token exchange: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("copilot token exchange: HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Token     string  `json:"token"`
		ExpiresAt float64 `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", time.Time{}, fmt.Errorf("copilot token exchange: decode: %w", err)
	}
	if payload.Token == "" {
		return "", time.Time{}, fmt.Errorf("copilot token exchange: empty token")
	}
	expiry := time.Now().Add(5 * time.Minute)
	if payload.ExpiresAt > 0 {
		expiry = time.Unix(int64(payload.ExpiresAt), 0)
	}
	return payload.Token, expiry, nil
}
