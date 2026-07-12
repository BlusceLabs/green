package gateway

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// LocalTransport is a Transport backed by an io.Reader/io.Writer (stdin/stdout
// by default). It is the reference transport: fully functional offline, used for
// development, testing, and shell piping. Each line on stdin is one message; the
// handler's reply (and any outbound the handler sends via Send) is written to
// stdout.
type LocalTransport struct {
	In     io.Reader
	Out    io.Writer
	Prompt string
	now    func() time.Time
	mu     sync.Mutex
	closed bool
}

// NewLocalTransport builds a LocalTransport reading from in and writing to out.
func NewLocalTransport(in io.Reader, out io.Writer) *LocalTransport {
	return &LocalTransport{In: in, Out: out, Prompt: "you> ", now: time.Now}
}

// Name implements Transport.
func (t *LocalTransport) Name() string { return "local" }

// Start implements Transport: reads lines from In, dispatches to handler, and
// prints replies to Out until the reader ends or ctx is cancelled.
func (t *LocalTransport) Start(ctx context.Context, handler Handler) error {
	scanner := bufio.NewScanner(t.In)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	t.writePrompt()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return err
			}
			return nil // EOF
		}
		line := strings.TrimRight(scanner.Text(), "\r\n")
		if strings.TrimSpace(line) == "" {
			t.writePrompt()
			continue
		}
		msg := Message{
			ID:        fmt.Sprintf("local-%d", t.now().UnixNano()),
			Platform:  "local",
			User:      "local",
			ChatID:    "local",
			Text:      line,
			Timestamp: t.now(),
		}
		reply, err := handler.Handle(ctx, msg)
		if err != nil {
			// Surface handler errors inline; the gateway also gets OnError.
			fmt.Fprintf(t.Out, "error: %v\n", err)
			t.writePrompt()
			continue
		}
		t.mu.Lock()
		fmt.Fprintf(t.Out, "green> %s\n", reply)
		t.mu.Unlock()
		t.writePrompt()
	}
}

// Send implements Transport: writes the outbound text to Out. For the local
// transport ChatID is ignored (single conversation).
func (t *LocalTransport) Send(ctx context.Context, msg Outbound) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, err := fmt.Fprintf(t.Out, "green> %s\n", msg.Text)
	return err
}

// Close implements Transport.
func (t *LocalTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	return nil
}

func (t *LocalTransport) writePrompt() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	fmt.Fprint(t.Out, t.Prompt)
}
