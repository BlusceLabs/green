package providermodelcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BlusceLabs/green/internal/providercatalog"
)

const (
	DefaultModelsDevURL = "https://models.dev/api.json"
	modelsDevSource     = "models.dev"
	openGatewaySource   = "opengateway"
)

type FetchOptions struct {
	HTTPClient     *http.Client
	ModelsDevURL   string
	OpenGatewayURL string
}

func FetchRemote(ctx context.Context, provider providercatalog.Descriptor, options FetchOptions) ([]Model, error) {
	if provider.ID == "gitlawb-opengateway" {
		models, err := FetchOpenGateway(ctx, defaultedOpenGatewayURL(provider, options.OpenGatewayURL), options)
		if err != nil {
			return nil, err
		}
		return models, nil
	}

	providerID := ModelsDevProviderID(provider)
	if providerID == "" {
		return nil, fmt.Errorf("provider %s does not have a models.dev catalog mapping", provider.ID)
	}
	return FetchModelsDev(ctx, providerID, options)
}

func FetchModelsDev(ctx context.Context, providerID string, options FetchOptions) ([]Model, error) {
	doc, err := loadModelsDevDoc(options)
	if err != nil {
		return nil, err
	}
	providerID = strings.TrimSpace(providerID)
	providerModels, ok := doc[providerID]
	if !ok {
		return nil, fmt.Errorf("models.dev provider %q not found", providerID)
	}
	return modelsDevProviderToModels(providerID, providerModels)
}

// modelsDevDocMemo caches the parsed models.dev api.json keyed by the URL it was
// fetched from, so the picker can enumerate many providers without re-downloading
// the (large) document on every provider switch. Entries are cached on success
// only — a failed fetch is retried on the next call, so a transient network error
// does not permanently poison the cache for the process.
var (
	modelsDevDocMu    sync.Mutex
	modelsDevDocCache = map[string]modelsDevDocEntry{}
)

type modelsDevDocEntry struct {
	doc map[string]map[string]remoteModel
	err error
}

func loadModelsDevDoc(options FetchOptions) (map[string]map[string]remoteModel, error) {
	url := strings.TrimSpace(options.ModelsDevURL)
	if url == "" {
		url = DefaultModelsDevURL
	}
	modelsDevDocMu.Lock()
	defer modelsDevDocMu.Unlock()
	if entry, ok := modelsDevDocCache[url]; ok {
		return entry.doc, entry.err
	}
	body, err := fetchJSON(context.Background(), url, options.HTTPClient)
	if err != nil {
		return nil, err
	}
	var payload map[string]struct {
		Models map[string]remoteModel `json:"models"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode models.dev catalog: %w", err)
	}
	doc := make(map[string]map[string]remoteModel, len(payload))
	for id, provider := range payload {
		doc[id] = provider.Models
	}
	modelsDevDocCache[url] = modelsDevDocEntry{doc: doc}
	return doc, nil
}

func FetchOpenGateway(ctx context.Context, endpoint string, options FetchOptions) ([]Model, error) {
	body, err := fetchJSON(ctx, endpoint, options.HTTPClient)
	if err != nil {
		return nil, err
	}
	return ParseOpenGatewayCatalog(body)
}

func ParseModelsDevProvider(body []byte, providerID string) ([]Model, error) {
	var payload map[string]struct {
		Models map[string]remoteModel `json:"models"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode models.dev catalog: %w", err)
	}
	providerID = strings.TrimSpace(providerID)
	provider, ok := payload[providerID]
	if !ok {
		return nil, fmt.Errorf("models.dev provider %q not found", providerID)
	}
	return modelsDevProviderToModels(providerID, provider.Models)
}

// modelsDevProviderToModels converts a provider's models.dev model map into the
// shared Model shape. It intentionally does NOT filter by IsCodingModel: the
// picker surfaces the full provider catalog (coding and non-coding) so users can
// see every model. Callers that need coding-only lists apply their own filter.
func modelsDevProviderToModels(providerID string, models map[string]remoteModel) ([]Model, error) {
	out := make([]Model, 0, len(models))
	for key, item := range models {
		model := item.toModel(key, modelsDevSource)
		if model.ID == "" {
			continue
		}
		out = append(out, model)
	}
	sortModels(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("models.dev provider %q returned no models", providerID)
	}
	return out, nil
}

func ParseOpenGatewayCatalog(body []byte) ([]Model, error) {
	var payload struct {
		Models []remoteModel `json:"models"`
		Data   []remoteModel `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode OpenGateway catalog: %w", err)
	}
	items := payload.Models
	if len(items) == 0 {
		items = payload.Data
	}
	models := make([]Model, 0, len(items))
	for _, item := range items {
		model := item.toModel("", openGatewaySource)
		if model.ID == "" {
			continue
		}
		models = append(models, model)
	}
	sortModels(models)
	if len(models) == 0 {
		return nil, fmt.Errorf("OpenGateway catalog returned no models")
	}
	return models, nil
}

func ModelsDevProviderID(provider providercatalog.Descriptor) string {
	switch strings.TrimSpace(provider.ID) {
	case "dashscope":
		return "alibaba"
	case "github":
		return "github-models"
	case "moonshot":
		return "moonshotai"
	case "nvidia-nim":
		return "nvidia"
	case "xiaomi-mimo":
		return "xiaomi"
	case "zai-cn":
		return "zai"
	case "minimaxi-cn":
		return "minimax"
	// Ollama Cloud is served from the same models.dev "ollama" catalog as the
	// local Ollama provider, so both resolve there to surface its model list.
	case "ollama", "ollama-cloud":
		return "ollama"
	default:
		return strings.TrimSpace(provider.ID)
	}
}

type remoteModel struct {
	ID                 string           `json:"id"`
	Name               string           `json:"name"`
	Description        string           `json:"description"`
	ContextWindow      int              `json:"context_window"`
	ContextWindowCamel int              `json:"contextWindow"`
	ToolCall           bool             `json:"tool_call"`
	ToolCallCamel      bool             `json:"toolCall"`
	Tools              bool             `json:"tools"`
	Reasoning          bool             `json:"reasoning"`
	InputCost          float64          `json:"input_cost"`
	OutputCost         float64          `json:"output_cost"`
	Tags               []string         `json:"tags"`
	Limit              remoteLimit      `json:"limit"`
	Cost               remoteCost       `json:"cost"`
	Modalities         remoteModalities `json:"modalities"`
}

type remoteLimit struct {
	Context int `json:"context"`
	Output  int `json:"output"`
}

type remoteCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}

type remoteModalities struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
}

func (model remoteModel) toModel(key string, source string) Model {
	id := firstNonEmpty(model.ID, key)
	contextWindow := firstPositive(model.ContextWindow, model.ContextWindowCamel, model.Limit.Context)
	inputCost := firstPositiveFloat(model.InputCost, model.Cost.Input)
	outputCost := firstPositiveFloat(model.OutputCost, model.Cost.Output)
	return Model{
		ID:               strings.TrimSpace(id),
		Description:      firstNonEmpty(model.Description, model.Name),
		ContextWindow:    contextWindow,
		ToolCall:         model.ToolCall || model.ToolCallCamel || model.Tools,
		Reasoning:        model.Reasoning,
		InputModalities:  cleanStrings(model.Modalities.Input),
		OutputModalities: cleanStrings(model.Modalities.Output),
		InputCost:        inputCost,
		OutputCost:       outputCost,
		Tags:             cleanStrings(model.Tags),
		Source:           source,
	}
}

func fetchJSON(ctx context.Context, endpoint string, client *http.Client) ([]byte, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("model catalog URL is required")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "green-cli")
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("model catalog returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func defaultedOpenGatewayURL(provider providercatalog.Descriptor, override string) string {
	if override = strings.TrimSpace(override); override != "" {
		return override
	}
	parsed, err := url.Parse(provider.DefaultBaseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "https://opengateway.gitlawb.com/green/models.json"
	}
	return parsed.Scheme + "://" + parsed.Host + "/green/models.json"
}

func sortModels(models []Model) {
	sort.SliceStable(models, func(i, j int) bool {
		left := modelSortLabel(models[i])
		right := modelSortLabel(models[j])
		if left == right {
			return models[i].ID < models[j].ID
		}
		return left < right
	})
}

func modelSortLabel(model Model) string {
	if label := strings.ToLower(strings.TrimSpace(model.Description)); label != "" {
		return label
	}
	return strings.ToLower(strings.TrimSpace(model.ID))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstPositiveFloat(values ...float64) float64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func cleanStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}
