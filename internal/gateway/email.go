package gateway

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/BlusceLabs/green/internal/redaction"
)

// EmailTransport is a Transport for email: it polls an IMAP mailbox for unseen
// messages and replies via SMTP. It is the universal, account-owned entry point
// (Hermes lists Email among its platforms). It is wired to a real mail server at
// runtime and is not unit-tested live.
type EmailTransport struct {
	IMAPServer string // host:port (TLS)
	SMTPHost   string // host:port (STARTTLS/TLS)
	User       string
	Password   string
	From       string // envelope From / reply-to
	Mailbox    string
	PollEvery  time.Duration

	conn net.Conn
	r    *bufio.Reader
	w    *bufio.Writer
	tag  int
	seen map[string]bool
}

// NewEmailTransport builds an email transport. from defaults to user when empty.
func NewEmailTransport(imapServer, smtpHost, user, password, from string) *EmailTransport {
	if from == "" {
		from = user
	}
	return &EmailTransport{
		IMAPServer: imapServer,
		SMTPHost:   smtpHost,
		User:       user,
		Password:   password,
		From:       from,
		Mailbox:    "INBOX",
		PollEvery:  30 * time.Second,
		seen:       map[string]bool{},
	}
}

// Name implements Transport.
func (t *EmailTransport) Name() string { return "email" }

func (t *EmailTransport) dial() error {
	conn, err := tls.Dial("tcp", t.IMAPServer, &tls.Config{ServerName: hostOnly(t.IMAPServer)})
	if err != nil {
		return fmt.Errorf("gateway: email imap dial: %w", err)
	}
	t.conn = conn
	t.r = bufio.NewReader(conn)
	t.w = bufio.NewWriter(conn)
	// Read the server greeting.
	if _, err := t.readResponse(); err != nil {
		return err
	}
	// LOGIN.
	if _, err := t.cmd("LOGIN %s %s", quote(t.User), quote(t.Password)); err != nil {
		return fmt.Errorf("gateway: email imap login: %w", err)
	}
	if _, err := t.cmd("SELECT %s", t.Mailbox); err != nil {
		return fmt.Errorf("gateway: email imap select: %w", err)
	}
	return nil
}

// Start implements Transport.
func (t *EmailTransport) Start(ctx context.Context, handler Handler) error {
	if strings.TrimSpace(t.IMAPServer) == "" || strings.TrimSpace(t.User) == "" {
		return fmt.Errorf("gateway: email transport requires an IMAP server, user, and password")
	}
	if err := t.dial(); err != nil {
		return err
	}
	defer t.Close()
	ticker := time.NewTicker(t.PollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		uids, err := t.searchUnseen(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Re-dial on transient IMAP errors.
			_ = t.Close()
			if derr := t.dial(); derr != nil {
				continue
			}
			continue
		}
		for _, uid := range uids {
			if t.seen[uid] {
				continue
			}
			t.seen[uid] = true
			raw, err := t.fetchRFC822(uid)
			if err != nil {
				continue
			}
			msg, perr := mail.ReadMessage(strings.NewReader(raw))
			if perr != nil {
				continue
			}
			from := msg.Header.Get("From")
			subject := msg.Header.Get("Subject")
			bodyBytes, _ := io.ReadAll(msg.Body)
			text := strings.TrimSpace(string(bodyBytes))
			if text == "" {
				continue
			}
			inbound := Message{
				ID:        uid,
				Platform:  "email",
				User:      from,
				ChatID:    from,
				Text:      text,
				Timestamp: time.Now(),
			}
			reply, herr := handler.Handle(ctx, inbound)
			if herr != nil {
				reply = "Sorry, I couldn't process that."
			}
			if strings.TrimSpace(reply) != "" {
				_ = t.Send(ctx, Outbound{Platform: "email", ChatID: from, Text: reply, Subject: "Re: " + subject})
			}
		}
	}
}

func (t *EmailTransport) searchUnseen(ctx context.Context) ([]string, error) {
	resp, err := t.cmd("UID SEARCH UNSEEN")
	if err != nil {
		return nil, err
	}
	var uids []string
	for _, line := range resp {
		if strings.Contains(line, "SEARCH") {
			fields := strings.Fields(line)
			for _, f := range fields {
				if _, e := strconv.Atoi(f); e == nil {
					uids = append(uids, f)
				}
			}
		}
	}
	return uids, nil
}

func (t *EmailTransport) fetchRFC822(uid string) (string, error) {
	resp, err := t.cmd("UID FETCH %s RFC822", uid)
	if err != nil {
		return "", err
	}
	for _, line := range resp {
		if strings.Contains(line, "RFC822 {") {
			// The literal body follows; readResponse already captured it into the
			// trailing element. Return the last element which holds the literal.
			if len(resp) > 0 {
				return redaction.RedactString(resp[len(resp)-1], redaction.Options{}), nil
			}
		}
	}
	// Fallback: join any captured literal text.
	if len(resp) > 0 {
		return resp[len(resp)-1], nil
	}
	return "", fmt.Errorf("gateway: email fetch yielded no body")
}

// Send implements Transport: sends the reply over SMTP.
func (t *EmailTransport) Send(ctx context.Context, msg Outbound) error {
	to, err := parseAddress(msg.ChatID)
	if err != nil {
		return err
	}
	subject := msg.Subject
	if subject == "" {
		subject = "green"
	}
	var body strings.Builder
	body.WriteString("From: " + t.From + "\r\n")
	body.WriteString("To: " + to + "\r\n")
	body.WriteString("Subject: " + subject + "\r\n")
	body.WriteString("MIME-Version: 1.0\r\n")
	body.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	body.WriteString("\r\n")
	body.WriteString(msg.Text)

	host := hostOnly(t.SMTPHost)
	port := portOnly(t.SMTPHost, "587")
	addr := net.JoinHostPort(host, port)
	c, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("gateway: email smtp dial: %w", err)
	}
	defer c.Quit()
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return fmt.Errorf("gateway: email smtp starttls: %w", err)
		}
	}
	if err := c.Auth(smtp.PlainAuth("", t.User, t.Password, host)); err != nil {
		return fmt.Errorf("gateway: email smtp auth: %w", err)
	}
	if err := c.Mail(t.From); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(body.String())); err != nil {
		return err
	}
	return w.Close()
}

// Close implements Transport.
func (t *EmailTransport) Close() error {
	if t.conn == nil {
		return nil
	}
	_, _ = t.cmd("LOGOUT")
	err := t.conn.Close()
	t.conn = nil
	return err
}

// --- minimal IMAP command helpers ---

func (t *EmailTransport) cmd(format string, args ...any) ([]string, error) {
	t.tag++
	tag := fmt.Sprintf("A%03d", t.tag)
	line := tag + " " + fmt.Sprintf(format, args...)
	if err := t.writeln(line); err != nil {
		return nil, err
	}
	return t.readResponse()
}

func (t *EmailTransport) readResponse() ([]string, error) {
	var out []string
	for {
		line, err := t.readln()
		if err != nil {
			return out, err
		}
		out = append(out, line)
		// A tagged completion line ends the response.
		if len(line) >= 4 && line[0] == 'A' && line[1] >= '0' && line[1] <= '9' &&
			(line[3] == ' ' || line[3] == '\r') {
			if strings.Contains(line, "OK") {
				return out, nil
			}
			if strings.Contains(line, "NO") || strings.Contains(line, "BAD") {
				return out, fmt.Errorf("gateway: email imap error: %s", line)
			}
		}
		// Literal continuation: a line ending in "{N}" means the next N bytes
		// are a literal (e.g. the message body) the server is sending us. Read
		// exactly N bytes and capture them as their own element.
		if strings.HasSuffix(strings.TrimSpace(line), "}") {
			if n, ok := literalSize(line); ok && n > 0 {
				buf := make([]byte, n)
				if _, rerr := io.ReadFull(t.r, buf); rerr == nil {
					out = append(out, string(buf))
				}
			}
			// The literal is followed by a closing ")" line (or trailing CRLF);
			// consume it and keep scanning for the tagged completion.
			_, _ = t.readln()
		}
	}
}

func (t *EmailTransport) readln() (string, error) {
	line, err := t.r.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (t *EmailTransport) writeln(line string) error {
	if _, err := t.w.WriteString(line + "\r\n"); err != nil {
		return err
	}
	return t.w.Flush()
}

func hostOnly(server string) string {
	if i := strings.LastIndex(server, ":"); i >= 0 {
		return server[:i]
	}
	return server
}

func portOnly(server, def string) string {
	if i := strings.LastIndex(server, ":"); i >= 0 {
		return server[i+1:]
	}
	return def
}

func parseAddress(header string) (string, error) {
	if strings.Contains(header, "<") {
		if start := strings.Index(header, "<"); start >= 0 {
			end := strings.Index(header, ">")
			if end > start {
				return strings.TrimSpace(header[start+1 : end]), nil
			}
		}
	}
	header = strings.TrimSpace(header)
	if header == "" {
		return "", fmt.Errorf("gateway: email empty recipient")
	}
	return header, nil
}

func quote(s string) string {
	return strconv.Quote(s)
}

// literalSize parses the byte count N from an IMAP literal marker "{N}".
func literalSize(line string) (int, bool) {
	start := strings.Index(line, "{")
	if start < 0 {
		return 0, false
	}
	end := strings.Index(line[start:], "}")
	if end < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(line[start+1 : start+end]))
	if err != nil {
		return 0, false
	}
	return n, true
}
