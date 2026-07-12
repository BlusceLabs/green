package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"
)

// SignalTransport is a Transport for Signal via signal-cli's JSON-RPC socket
// (signal-cli in daemon mode, listening on 127.0.0.1:7583 by default). It
// subscribes for inbound envelopes and sends via the "send" method. It is wired
// to a running signal-cli daemon at runtime and is not unit-tested live.
type SignalTransport struct {
	Account   string // your Signal number, e.g. "+14155238886"
	Socket    string // host:port of the signal-cli JSON-RPC socket
	conn      net.Conn
	enc       *json.Encoder
	dec       *bufio.Scanner
	requestID int
}

// NewSignalTransport builds a Signal transport for the given account number.
func NewSignalTransport(account, socket string) *SignalTransport {
	if socket == "" {
		socket = "127.0.0.1:7583"
	}
	return &SignalTransport{Account: account, Socket: socket}
}

// Name implements Transport.
func (t *SignalTransport) Name() string { return "signal" }

func (t *SignalTransport) dial() error {
	if strings.TrimSpace(t.Account) == "" {
		return fmt.Errorf("gateway: signal transport requires an account number")
	}
	conn, err := net.Dial("tcp", t.Socket)
	if err != nil {
		return fmt.Errorf("gateway: signal-cli socket dial: %w", err)
	}
	t.conn = conn
	t.enc = json.NewEncoder(conn)
	t.dec = bufio.NewScanner(conn)
	t.dec.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	return nil
}

type signalRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type signalReceive struct {
	Method string `json:"method"`
	Params struct {
		Envelope struct {
			Source      string `json:"source"`
			DataMessage struct {
				Message string `json:"message"`
			} `json:"dataMessage"`
		} `json:"envelope"`
	} `json:"params"`
}

// Start implements Transport.
func (t *SignalTransport) Start(ctx context.Context, handler Handler) error {
	if err := t.dial(); err != nil {
		return err
	}
	defer t.Close()

	t.requestID++
	if err := t.enc.Encode(signalRequest{
		JSONRPC: "2.0",
		ID:      t.requestID,
		Method:  "subscribe",
		Params:  map[string]any{"accounts": []string{t.Account}},
	}); err != nil {
		return fmt.Errorf("gateway: signal subscribe: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if !t.dec.Scan() {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("gateway: signal socket closed")
		}
		line := strings.TrimSpace(t.dec.Text())
		if line == "" {
			continue
		}
		var recv signalReceive
		if err := json.Unmarshal([]byte(line), &recv); err != nil {
			continue
		}
		if recv.Method != "receive" {
			continue
		}
		text := strings.TrimSpace(recv.Params.Envelope.DataMessage.Message)
		if text == "" {
			continue
		}
		inbound := Message{
			ID:        fmt.Sprintf("signal-%d", time.Now().UnixNano()),
			Platform:  "signal",
			User:      recv.Params.Envelope.Source,
			ChatID:    recv.Params.Envelope.Source,
			Text:      text,
			Timestamp: time.Now(),
		}
		reply, herr := handler.Handle(ctx, inbound)
		if herr != nil {
			reply = "Sorry, I couldn't process that."
		}
		if strings.TrimSpace(reply) != "" {
			_ = t.Send(ctx, Outbound{Platform: "signal", ChatID: inbound.ChatID, Text: reply})
		}
	}
}

// Send implements Transport: sends via the signal-cli "send" method.
func (t *SignalTransport) Send(ctx context.Context, msg Outbound) error {
	t.requestID++
	return t.enc.Encode(signalRequest{
		JSONRPC: "2.0",
		ID:      t.requestID,
		Method:  "send",
		Params: map[string]any{
			"account":   t.Account,
			"recipient": []string{msg.ChatID},
			"message":   msg.Text,
		},
	})
}

// Close implements Transport.
func (t *SignalTransport) Close() error {
	if t.conn == nil {
		return nil
	}
	err := t.conn.Close()
	t.conn = nil
	return err
}
