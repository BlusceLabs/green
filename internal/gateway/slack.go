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

	"github.com/coder/websocket"
)

// SlackTransport is a Transport for Slack using Socket Mode (websocket). It
// opens a connection via apps.connections.open with the app-level token, then
// receives event envelopes (which must be ACKed on the same socket) and replies
// with chat.postMessage using the bot token. It is wired to the real Slack API
// at runtime and is not unit-tested live.
//
// Required credentials:
//   - AppLevelToken: "xapp-..." token (Slack App-Level Token, with connections:write)
//   - BotToken:       "xoxb-..." bot token (with chat:write, im:history, etc.)
type SlackTransport struct {
	AppLevelToken string
	BotToken      string
	OpenURL       string
	client        *http.Client
}

// NewSlackTransport builds a Slack Socket Mode transport.
func NewSlackTransport(appLevelToken, botToken string) *SlackTransport {
	return &SlackTransport{
		AppLevelToken: appLevelToken,
		BotToken:      botToken,
		OpenURL:       "https://slack.com/api/apps.connections.open",
		client:        &http.Client{Timeout: 30 * time.Second},
	}
}

// Name implements Transport.
func (t *SlackTransport) Name() string { return "slack" }

type slackOpenResponse struct {
	OK        bool   `json:"ok"`
	Error     string `json:"error"`
	URL       string `json:"url"`
	WebSocket string `json:"websocket_url"`
}

type slackEnvelope struct {
	Type         string          `json:"type"`
	EnvelopeID   string          `json:"envelope_id"`
	Payload      json.RawMessage `json:"payload"`
	WebSocketURL string          `json:"websocket_url"`
}

type slackEventMessage struct {
	Type        string `json:"type"`
	ChannelType string `json:"channel_type"`
	Channel     string `json:"channel"`
	User        string `json:"user"`
	BotID       string `json:"bot_id"`
	Text        string `json:"text"`
	TS          string `json:"ts"`
}

// openConnection calls apps.connections.open to obtain the websocket URL.
func (t *SlackTransport) openConnection(ctx context.Context) (string, error) {
	body, err := json.Marshal(map[string]any{})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.OpenURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+t.AppLevelToken)
	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("gateway: slack open: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var out slackOpenResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("gateway: slack open decode: %w", err)
	}
	if !out.OK || (out.WebSocket == "" && out.URL == "") {
		return "", fmt.Errorf("gateway: slack open failed: %s", out.Error)
	}
	if out.WebSocket != "" {
		return out.WebSocket, nil
	}
	return out.URL, nil
}

// Start implements Transport.
func (t *SlackTransport) Start(ctx context.Context, handler Handler) error {
	if strings.TrimSpace(t.AppLevelToken) == "" || strings.TrimSpace(t.BotToken) == "" {
		return fmt.Errorf("gateway: slack transport requires an app-level token and a bot token")
	}
	wsURL, err := t.openConnection(ctx)
	if err != nil {
		return err
	}
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("gateway: slack dial: %w", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "bye")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		_, data, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("gateway: slack read: %w", err)
		}
		var env slackEnvelope
		if err := json.Unmarshal(data, &env); err != nil {
			continue
		}
		// Connection URL refresh envelopes just update the URL; ignore here.
		if env.Type == "disconnect" || env.WebSocketURL != "" {
			continue
		}
		// Every non-hello envelope must be ACKed on the same socket.
		if env.EnvelopeID != "" {
			ack, _ := json.Marshal(map[string]any{
				"envelope_id": env.EnvelopeID,
				"type":        "ack",
			})
			_ = conn.Write(ctx, websocket.MessageText, ack)
		}
		if env.Type != "events" {
			continue
		}
		var ev slackEventMessage
		if err := json.Unmarshal(env.Payload, &ev); err != nil {
			continue
		}
		if ev.Type != "message" || ev.BotID != "" || ev.User == "" {
			continue // skip bot's own messages and non-message events
		}
		inbound := Message{
			ID:        fmt.Sprintf("slack-%s", ev.TS),
			Platform:  "slack",
			User:      ev.User,
			ChatID:    ev.Channel,
			Text:      ev.Text,
			Timestamp: time.Now(),
		}
		reply, herr := handler.Handle(ctx, inbound)
		if herr != nil {
			reply = "Sorry, I couldn't process that."
		}
		if strings.TrimSpace(reply) != "" {
			_ = t.Send(ctx, Outbound{Platform: "slack", ChatID: ev.Channel, Text: reply})
		}
	}
}

// Send implements Transport: posts the reply via chat.postMessage.
func (t *SlackTransport) Send(ctx context.Context, msg Outbound) error {
	body, err := json.Marshal(map[string]any{"channel": msg.ChatID, "text": msg.Text})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/chat.postMessage", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+t.BotToken)
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("gateway: slack send: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var out struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(data, &out); err == nil && !out.OK {
		return fmt.Errorf("gateway: slack send failed: %s", out.Error)
	}
	return nil
}

// Close implements Transport.
func (t *SlackTransport) Close() error { return nil }
