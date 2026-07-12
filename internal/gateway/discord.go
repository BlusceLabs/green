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

// DiscordTransport is a Transport for the Discord Gateway (bot API). It connects
// over a websocket, identifies with the bot token, heartbeats, and dispatches
// MESSAGE_CREATE events to the handler, replying in the same channel. It is
// wired to the real Discord Gateway at runtime and is not unit-tested live.
type DiscordTransport struct {
	Token   string
	Gateway string
	Intents int
	client  *http.Client
}

// NewDiscordTransport builds a Discord transport for the given bot token (the
// raw token; the "Bot " prefix is added on the wire).
func NewDiscordTransport(token string) *DiscordTransport {
	intents := 1<<9 | 1<<12 // GUILD_MESSAGES | DIRECT_MESSAGES
	return &DiscordTransport{
		Token:   token,
		Gateway: "wss://gateway.discord.gg/?v=10&encoding=json",
		Intents: intents,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// Name implements Transport.
func (t *DiscordTransport) Name() string { return "discord" }

type discordMessage struct {
	Op   int             `json:"op"`
	Data json.RawMessage `json:"d"`
	Type string          `json:"t"`
	Seq  *int            `json:"s"`
}

type discordHello struct {
	HeartbeatInterval int `json:"heartbeat_interval"`
}

type discordIdentify struct {
	Token      string            `json:"token"`
	Intents    int               `json:"intents"`
	Properties map[string]string `json:"properties"`
}

// Start implements Transport.
func (t *DiscordTransport) Start(ctx context.Context, handler Handler) error {
	if strings.TrimSpace(t.Token) == "" {
		return fmt.Errorf("gateway: discord transport requires a bot token")
	}
	conn, _, err := websocket.Dial(ctx, t.Gateway, nil)
	if err != nil {
		return fmt.Errorf("gateway: discord dial: %w", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "bye")

	var lastSeq *int
	heartbeat := make(chan struct{}, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-heartbeat:
				payload, _ := json.Marshal(map[string]any{"op": 1, "d": lastSeq})
				_ = conn.Write(ctx, websocket.MessageText, payload)
			}
		}
	}()

	// Read the HELLO, learn the heartbeat interval, then IDENTIFY.
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("gateway: discord read: %w", err)
		}
		var msg discordMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg.Op == 10 { // HELLO
			var hello discordHello
			_ = json.Unmarshal(msg.Data, &hello)
			interval := time.Duration(hello.HeartbeatInterval) * time.Millisecond
			if interval <= 0 {
				interval = 30 * time.Second
			}
			identify, _ := json.Marshal(map[string]any{
				"op": 2,
				"d": discordIdentify{
					Token:   t.Token,
					Intents: t.Intents,
					Properties: map[string]string{
						"$os":      "linux",
						"$browser": "green",
						"$device":  "green",
					},
				},
			})
			if err := conn.Write(ctx, websocket.MessageText, identify); err != nil {
				return fmt.Errorf("gateway: discord identify: %w", err)
			}
			go func() {
				ticker := time.NewTicker(interval)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						select {
						case heartbeat <- struct{}{}:
						default:
						}
					}
				}
			}()
			break
		}
	}

	// Main dispatch loop.
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
			return fmt.Errorf("gateway: discord read: %w", err)
		}
		var msg discordMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg.Seq != nil {
			lastSeq = msg.Seq
		}
		if msg.Op != 0 || msg.Type != "MESSAGE_CREATE" {
			continue
		}
		var ev struct {
			Content   string `json:"content"`
			ChannelID string `json:"channel_id"`
			Channel   struct {
				ID string `json:"id"`
			} `json:"channel"`
			Author struct {
				Bot      bool   `json:"bot"`
				ID       string `json:"id"`
				Username string `json:"username"`
			} `json:"author"`
		}
		if err := json.Unmarshal(msg.Data, &ev); err != nil {
			continue
		}
		if ev.Author.Bot {
			continue
		}
		chatID := ev.ChannelID
		if chatID == "" {
			chatID = ev.Channel.ID
		}
		inbound := Message{
			ID:        fmt.Sprintf("discord-%d", derefSeq(msg.Seq)),
			Platform:  "discord",
			User:      ev.Author.Username,
			ChatID:    chatID,
			Text:      ev.Content,
			Timestamp: time.Now(),
		}
		reply, herr := handler.Handle(ctx, inbound)
		if herr != nil {
			reply = "Sorry, I couldn't process that."
		}
		if strings.TrimSpace(reply) != "" {
			_ = t.Send(ctx, Outbound{Platform: "discord", ChatID: chatID, Text: reply})
		}
	}
}

// Send implements Transport: posts the reply to the Discord channel via REST.
func (t *DiscordTransport) Send(ctx context.Context, msg Outbound) error {
	body, err := json.Marshal(map[string]any{"content": msg.Text})
	if err != nil {
		return err
	}
	url := fmt.Sprintf("https://discord.com/api/v10/channels/%s/messages", msg.ChatID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bot "+t.Token)
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("gateway: discord send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gateway: discord send failed (%d): %s", resp.StatusCode, string(data))
	}
	return nil
}

// Close implements Transport.
func (t *DiscordTransport) Close() error { return nil }

func derefSeq(s *int) int {
	if s == nil {
		return 0
	}
	return *s
}
