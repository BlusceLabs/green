package gateway

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"
)

// WhatsAppTransport bridges green to WhatsApp using the whatsmeow multidevice
// protocol. The device session is persisted in a SQLite store under DataDir.
// On first run the transport prints a QR code payload that the user scans with
// WhatsApp (Settings > Linked Devices).
type WhatsAppTransport struct {
	DataDir   string
	PairPhone string

	handler Handler
	client  *whatsmeow.Client
}

// NewWhatsAppTransport creates a WhatsApp transport. DataDir holds the device
// session database; if empty a default under the user config dir is used.
// PairPhone, when non-empty, initiates phone-number pairing instead of QR.
func NewWhatsAppTransport(dataDir, pairPhone string) *WhatsAppTransport {
	if dataDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dataDir = filepath.Join(home, ".local", "share", "green", "whatsmeow")
		} else {
			dataDir = filepath.Join(os.TempDir(), "green-whatsmeow")
		}
	}
	return &WhatsAppTransport{DataDir: dataDir, PairPhone: pairPhone}
}

func (t *WhatsAppTransport) Name() string { return "whatsapp" }

func (t *WhatsAppTransport) Start(ctx context.Context, h Handler) error {
	t.handler = h
	if err := os.MkdirAll(t.DataDir, 0o700); err != nil {
		return fmt.Errorf("whatsapp: create data dir: %w", err)
	}

	dbPath := filepath.Join(t.DataDir, "device.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(ON)&_busy_timeout=5000")
	if err != nil {
		return fmt.Errorf("whatsapp: open device db: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		return fmt.Errorf("whatsapp: enable foreign keys: %w", err)
	}

	container := sqlstore.NewWithDB(db, "sqlite", nil)
	if err := container.Upgrade(ctx); err != nil {
		return fmt.Errorf("whatsapp: upgrade schema: %w", err)
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		return fmt.Errorf("whatsapp: load device: %w", err)
	}

	client := whatsmeow.NewClient(device, nil)
	t.client = client
	client.AddEventHandler(t.handleEvent)

	if client.Store.ID == nil {
		if strings.TrimSpace(t.PairPhone) != "" {
			if err := t.pairByPhone(ctx, client); err != nil {
				return err
			}
		} else {
			if err := t.pairByQR(ctx, client); err != nil {
				return err
			}
		}
	}

	if err := client.Connect(); err != nil {
		return fmt.Errorf("whatsapp: connect: %w", err)
	}
	fmt.Fprintln(os.Stdout, "whatsapp: connected")

	<-ctx.Done()
	client.Disconnect()
	return nil
}

func (t *WhatsAppTransport) pairByQR(ctx context.Context, client *whatsmeow.Client) error {
	qrChan, err := client.GetQRChannel(ctx)
	if err != nil {
		return fmt.Errorf("whatsapp: qr channel: %w", err)
	}
	go func() {
		for evt := range qrChan {
			switch evt.Event {
			case "code":
				fmt.Fprintln(os.Stdout, "whatsapp: scan this QR code with WhatsApp (Settings > Linked Devices):")
				fmt.Fprintln(os.Stdout, evt.Code)
			case "success":
				fmt.Fprintln(os.Stdout, "whatsapp: device linked")
			case "error":
				fmt.Fprintf(os.Stderr, "whatsapp: pairing error: %v\n", evt.Error)
			}
		}
	}()
	return nil
}

func (t *WhatsAppTransport) pairByPhone(ctx context.Context, client *whatsmeow.Client) error {
	code, err := client.PairPhone(ctx, t.PairPhone, true, whatsmeow.PairClientOtherWebClient, "green")
	if err != nil {
		return fmt.Errorf("whatsapp: request pairing code: %w", err)
	}
	fmt.Fprintf(os.Stdout, "whatsapp: enter this code in WhatsApp to finish linking: %s\n", code)
	return nil
}

func (t *WhatsAppTransport) handleEvent(evt any) {
	m, ok := evt.(*events.Message)
	if !ok {
		return
	}
	if m.Info.IsFromMe {
		return
	}
	text := whatsappText(m.Message)
	if strings.TrimSpace(text) == "" {
		return
	}
	in := Message{
		Platform: "whatsapp",
		ChatID:   m.Info.Chat.String(),
		From:     m.Info.Sender.String(),
		Text:     text,
	}
	reply, err := t.handler.Handle(context.Background(), in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "whatsapp: handler error: %v\n", err)
		return
	}
	if strings.TrimSpace(reply) == "" {
		return
	}
	if _, err := t.client.SendMessage(context.Background(), m.Info.Chat, &waE2E.Message{
		Conversation: proto.String(reply),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "whatsapp: send error: %v\n", err)
	}
}

func whatsappText(msg *waE2E.Message) string {
	if msg == nil {
		return ""
	}
	if c := msg.GetConversation(); c != "" {
		return c
	}
	if ext := msg.GetExtendedTextMessage(); ext != nil {
		return ext.GetText()
	}
	return ""
}

// Send delivers an outbound message to a WhatsApp chat identified by JID string.
func (t *WhatsAppTransport) Send(ctx context.Context, ob Outbound) error {
	if t.client == nil {
		return fmt.Errorf("whatsapp: not connected")
	}
	if strings.TrimSpace(ob.Text) == "" {
		return nil
	}
	jid, err := types.ParseJID(ob.ChatID)
	if err != nil {
		return fmt.Errorf("whatsapp: parse chat jid %q: %w", ob.ChatID, err)
	}
	_, err = t.client.SendMessage(ctx, jid, &waE2E.Message{
		Conversation: proto.String(ob.Text),
	})
	if err != nil {
		return fmt.Errorf("whatsapp: send: %w", err)
	}
	return nil
}

func (t *WhatsAppTransport) Close() error {
	if t.client != nil {
		t.client.Disconnect()
	}
	return nil
}

var _ Transport = (*WhatsAppTransport)(nil)
