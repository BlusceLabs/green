package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// TelegramTransport is a Transport for the Telegram Bot API using long-polling
// (getUpdates). It requires a bot token (from @BotFather). It is wiring-complete
// and uses only the standard library; it is exercised against the real API at
// runtime and is not unit-tested live (no token in CI).
type TelegramTransport struct {
	Token   string
	BaseURL string
	client  *http.Client
	offset  int64
	// PollTimeout is the getUpdates long-poll timeout (seconds).
	PollTimeout int
	// Access, when set, gates inbound messages by an admin/member allow-list
	// with a pairing-code request flow (see telegram_access.go). Nil disables
	// gating (any sender is handled).
	Access *AccessStore
}

// NewTelegramTransport builds a Telegram transport for the given bot token.
func NewTelegramTransport(token string) *TelegramTransport {
	return &TelegramTransport{
		Token:       token,
		BaseURL:     "https://api.telegram.org",
		client:      &http.Client{Timeout: 60 * time.Second},
		PollTimeout: 30,
	}
}

// Name implements Transport.
func (t *TelegramTransport) Name() string { return "telegram" }

func (t *TelegramTransport) apiURL(method string) string {
	base := strings.TrimRight(t.BaseURL, "/")
	return fmt.Sprintf("%s/bot%s/%s", base, t.Token, method)
}

// Start implements Transport: long-polls Telegram for updates and dispatches
// each message to handler, posting the reply back to the same chat.
func (t *TelegramTransport) Start(ctx context.Context, handler Handler) error {
	if strings.TrimSpace(t.Token) == "" {
		return fmt.Errorf("gateway: telegram transport requires a bot token")
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		updates, err := t.poll(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Transient API error: back off and retry rather than crash.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
			}
			continue
		}
		for _, update := range updates {
			if update.Message == nil {
				continue
			}
			msg := Message{
				ID:        fmt.Sprintf("%d", update.UpdateID),
				Platform:  "telegram",
				User:      update.Message.From.Username,
				From:      fmt.Sprintf("%d", update.Message.From.ID),
				ChatID:    fmt.Sprintf("%d", update.Message.Chat.ID),
				Text:      update.Message.Text,
				Timestamp: time.Now(),
			}
			// Access control: /start requests access; unapproved senders are held
			// until an admin approves their pairing code. Approved senders fall
			// through to the normal handler.
			if t.Access != nil {
				if gated, gateReply := t.gateMessage(msg); gated {
					if strings.TrimSpace(gateReply) != "" {
						_ = t.Send(ctx, Outbound{Platform: "telegram", ChatID: msg.ChatID, Text: gateReply})
					}
					t.offset = update.UpdateID + 1
					continue
				}
			}
			reply, herr := handler.Handle(ctx, msg)
			if herr != nil {
				reply = "Sorry, I couldn't process that."
			}
			if strings.TrimSpace(reply) != "" {
				if serr := t.Send(ctx, Outbound{Platform: "telegram", ChatID: msg.ChatID, Text: reply}); serr != nil {
					// Non-fatal: keep processing remaining updates.
					continue
				}
			}
			t.offset = update.UpdateID + 1
		}
	}
}

// Send implements Transport: posts the reply to the given Telegram chat.
func (t *TelegramTransport) Send(ctx context.Context, msg Outbound) error {
	chatID := msg.ChatID
	body, err := json.Marshal(map[string]any{
		"chat_id": chatID,
		"text":    msg.Text,
		// Disable link previews so tool output isn't mangled.
		"disable_web_page_preview": true,
	})
	if err != nil {
		return fmt.Errorf("gateway: telegram encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.apiURL("sendMessage"), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("gateway: telegram send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gateway: telegram send failed (%d): %s", resp.StatusCode, string(data))
	}
	return nil
}

// Close implements Transport.
func (t *TelegramTransport) Close() error { return nil }

func (t *TelegramTransport) poll(ctx context.Context) ([]telegramUpdate, error) {
	url := fmt.Sprintf("%s?offset=%d&timeout=%d", t.apiURL("getUpdates"), t.offset, t.PollTimeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram getUpdates failed (%d): %s", resp.StatusCode, string(data))
	}
	var out struct {
		OK     bool             `json:"ok"`
		Result []telegramUpdate `json:"result"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("telegram decode: %w", err)
	}
	return out.Result, nil
}

type telegramUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		Text string `json:"text"`
		From struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"from"`
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
	} `json:"message"`
}

// gateMessage applies the access-control policy to an inbound message. It
// returns (gated=true, reply) when the message should NOT reach the handler
// (access request, pending, or unapproved), and (false, "") when the sender is
// approved and the message proceeds to the handler.
func (t *TelegramTransport) gateMessage(msg Message) (bool, string) {
	userID := msg.From
	username := msg.User
	if strings.TrimSpace(msg.Text) == "/start" {
		if t.Access.IsApproved(userID) {
			return true, "You already have access to this bot."
		}
		code, _ := t.Access.RequestAccess(username, userID)
		return true, fmt.Sprintf("Access requested. Share this code with an admin to be approved: %s", code)
	}
	if !t.Access.IsApproved(userID) {
		if t.Access.IsPending(userID) {
			return true, "Your access request is pending admin approval."
		}
		return true, "You don't have access yet. Send /start to request access."
	}
	return false, ""
}
