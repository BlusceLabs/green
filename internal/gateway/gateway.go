// Package gateway implements Hermes's messaging entry points: a single gateway
// process that lets the agent be reached from chat platforms (Telegram, Discord,
// Slack, WhatsApp, Signal, Email) with conversation continuity across them.
//
// The design is transport-pluggable: the Gateway owns the conversation loop and
// a Handler turns an inbound message into a reply; each platform is a Transport
// that knows how to receive messages and send replies on that platform. green
// ships a Local transport (stdin/stdout, fully testable) and a Telegram
// transport (Bot API long-polling). Additional platforms implement the same
// Transport interface.
package gateway

import (
	"context"
	"fmt"
	"time"
)

// Message is a normalized inbound message from any platform.
type Message struct {
	ID        string
	Platform  string
	User      string
	ChatID    string
	Text      string
	Timestamp time.Time
	// From is the platform-specific sender identifier (optional).
	From string
}

// Outbound is a normalized reply to send on a platform.
type Outbound struct {
	Platform string
	ChatID   string
	Text     string
	// Subject is an optional email subject (ignored by other platforms).
	Subject string
}

// Handler turns an inbound message into a reply. It is the single seam between
// the gateway and the agent: the CLI wires an agent run here.
type Handler interface {
	Handle(ctx context.Context, msg Message) (string, error)
}

// HandlerFunc adapts a function to the Handler interface.
type HandlerFunc func(ctx context.Context, msg Message) (string, error)

// Handle implements Handler.
func (f HandlerFunc) Handle(ctx context.Context, msg Message) (string, error) {
	return f(ctx, msg)
}

// Transport receives messages from a platform and sends replies back. Start
// blocks, delivering each inbound message to handler, until ctx is cancelled.
type Transport interface {
	// Name is the platform identifier (e.g. "telegram", "local").
	Name() string
	// Start runs the receive loop until ctx is done.
	Start(ctx context.Context, handler Handler) error
	// Send posts a reply.
	Send(ctx context.Context, msg Outbound) error
	// Close releases transport resources.
	Close() error
}

// Gateway binds a Transport to a Handler and runs the conversation loop.
type Gateway struct {
	Transport Transport
	Handler   Handler
	// OnError, when set, is notified of per-message handler errors so the
	// gateway can keep running instead of dying on one bad turn.
	OnError func(msg Message, err error)
}

// Run starts the gateway. It returns when the transport's receive loop ends
// (usually on context cancellation).
func (g *Gateway) Run(ctx context.Context) error {
	if g.Transport == nil {
		return fmt.Errorf("gateway: no transport configured")
	}
	if g.Handler == nil {
		return fmt.Errorf("gateway: no handler configured")
	}
	wrapped := HandlerFunc(func(ctx context.Context, msg Message) (string, error) {
		reply, err := g.Handler.Handle(ctx, msg)
		if err != nil {
			if g.OnError != nil {
				g.OnError(msg, err)
			}
			return "Sorry, something went wrong processing that message.", err
		}
		return reply, nil
	})
	return g.Transport.Start(ctx, wrapped)
}

// Send posts a reply through the configured transport.
func (g *Gateway) Send(ctx context.Context, msg Outbound) error {
	if g.Transport == nil {
		return fmt.Errorf("gateway: no transport configured")
	}
	return g.Transport.Send(ctx, msg)
}

// Close releases the transport.
func (g *Gateway) Close() error {
	if g.Transport == nil {
		return nil
	}
	return g.Transport.Close()
}
