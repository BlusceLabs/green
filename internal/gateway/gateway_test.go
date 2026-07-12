package gateway

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// echoHandler replies with the uppercased input.
func echoHandler(ctx context.Context, msg Message) (string, error) {
	return strings.ToUpper(msg.Text), nil
}

func TestLocalTransportRoundTrip(t *testing.T) {
	in := strings.NewReader("hello\nworld\n")
	out := &safeBuffer{}
	tr := NewLocalTransport(in, out)
	g := &Gateway{Transport: tr, Handler: HandlerFunc(echoHandler)}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.Run(ctx) }()

	// Give the loop a moment, then stop it by cancelling once input drains.
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("gateway did not exit")
	}
	got := out.String()
	if !strings.Contains(got, "green> HELLO") || !strings.Contains(got, "green> WORLD") {
		t.Fatalf("unexpected gateway output: %q", got)
	}
}

// stubTransport records sent messages and feeds scripted inbound messages.
type stubTransport struct {
	name    string
	inbound []Message
	sent    []Outbound
}

func (s *stubTransport) Name() string { return s.name }
func (s *stubTransport) Start(ctx context.Context, h Handler) error {
	for _, m := range s.inbound {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		reply, err := h.Handle(ctx, m)
		if err != nil {
			return err
		}
		_ = s.Send(ctx, Outbound{Platform: m.Platform, ChatID: m.ChatID, Text: reply})
	}
	<-ctx.Done()
	return ctx.Err()
}
func (s *stubTransport) Send(ctx context.Context, m Outbound) error {
	s.sent = append(s.sent, m)
	return nil
}
func (s *stubTransport) Close() error { return nil }

func TestGatewayUsesHandler(t *testing.T) {
	stub := &stubTransport{
		name: "test",
		inbound: []Message{
			{ID: "1", Platform: "test", User: "u", ChatID: "c", Text: "ping"},
		},
	}
	g := &Gateway{Transport: stub, Handler: HandlerFunc(echoHandler)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = g.Run(ctx) }()
	deadline := time.After(2 * time.Second)
	for len(stub.sent) == 0 {
		select {
		case <-deadline:
			t.Fatal("handler was not invoked")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if stub.sent[0].Text != "PING" {
		t.Fatalf("expected PING reply, got %+v", stub.sent)
	}
}

func TestGatewayOnError(t *testing.T) {
	fail := HandlerFunc(func(ctx context.Context, m Message) (string, error) {
		return "", context.DeadlineExceeded
	})
	stub := &stubTransport{name: "test", inbound: []Message{{ID: "1", ChatID: "c", Text: "x"}}}
	var gotErr error
	g := &Gateway{Transport: stub, Handler: fail, OnError: func(m Message, err error) { gotErr = err }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = g.Run(ctx) }()
	deadline := time.After(2 * time.Second)
	for gotErr == nil {
		select {
		case <-deadline:
			t.Fatal("OnError did not fire")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// safeBuffer is a thread-safe strings.Builder substitute for capturing output.
type safeBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}
func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
