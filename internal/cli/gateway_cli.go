package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/BlusceLabs/green/internal/gateway"
)

func runGateway(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	command := "start"
	rest := args
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			return writeGatewayHelp(stdout)
		case "start", "list", "ls":
			command = args[0]
			rest = args[1:]
		default:
			return writeExecUsageError(stderr, fmt.Sprintf("unknown gateway subcommand %q", args[0]))
		}
	}
	switch command {
	case "list", "ls":
		fmt.Fprintln(stdout, "Available transports:")
		fmt.Fprintln(stdout, "  local     stdin/stdout (offline, testable)")
		fmt.Fprintln(stdout, "  telegram  Telegram Bot API (requires a bot token)")
		fmt.Fprintln(stdout, "  discord   Discord Gateway (requires a bot token)")
		fmt.Fprintln(stdout, "  slack     Slack Socket Mode (requires app-level + bot tokens)")
		fmt.Fprintln(stdout, "  whatsapp  WhatsApp via whatsmeow (native multidevice; QR/phone pairing)")
		fmt.Fprintln(stdout, "  email     IMAP + SMTP (requires IMAP server, user, password)")
		fmt.Fprintln(stdout, "  signal    Signal via signal-cli daemon socket (requires account)")
		return exitSuccess
	case "start":
		return runGatewayStart(rest, stdout, stderr, deps)
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown gateway subcommand %q", command))
	}
}

func runGatewayStart(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	transport := "local"
	var token, appToken string
	for _, a := range args {
		switch {
		case a == "--transport" && len(args) > 1:
		case strings.HasPrefix(a, "--transport="):
			transport = strings.TrimPrefix(a, "--transport=")
		case strings.HasPrefix(a, "--token="):
			token = strings.TrimPrefix(a, "--token=")
		case strings.HasPrefix(a, "--app-token="):
			appToken = strings.TrimPrefix(a, "--app-token=")
		case a == "-h" || a == "--help" || a == "help":
			return writeGatewayHelp(stdout)
		}
	}

	var t gateway.Transport
	switch transport {
	case "local", "":
		t = gateway.NewLocalTransport(os.Stdin, stdout)
	case "telegram":
		if strings.TrimSpace(token) == "" {
			token = os.Getenv("GREEN_TELEGRAM_TOKEN")
		}
		if strings.TrimSpace(token) == "" {
			return writeExecUsageError(stderr, "telegram transport requires --token or GREEN_TELEGRAM_TOKEN")
		}
		t = gateway.NewTelegramTransport(token)
	case "discord":
		if strings.TrimSpace(token) == "" {
			token = os.Getenv("GREEN_DISCORD_TOKEN")
		}
		if strings.TrimSpace(token) == "" {
			return writeExecUsageError(stderr, "discord transport requires --token or GREEN_DISCORD_TOKEN")
		}
		t = gateway.NewDiscordTransport(token)
	case "slack":
		if strings.TrimSpace(token) == "" {
			token = os.Getenv("GREEN_SLACK_BOT_TOKEN")
		}
		if strings.TrimSpace(appToken) == "" {
			appToken = os.Getenv("GREEN_SLACK_APP_TOKEN")
		}
		if strings.TrimSpace(token) == "" || strings.TrimSpace(appToken) == "" {
			return writeExecUsageError(stderr, "slack transport requires --token (bot) and --app-token (app-level)")
		}
		t = gateway.NewSlackTransport(appToken, token)
	case "whatsapp":
		dataDir := os.Getenv("GREEN_WHATSMEOW_DIR")
		pairPhone := os.Getenv("GREEN_WHATSAPP_PAIR_PHONE")
		for _, a := range args {
			switch {
			case strings.HasPrefix(a, "--data-dir="):
				dataDir = strings.TrimPrefix(a, "--data-dir=")
			case strings.HasPrefix(a, "--pair-phone="):
				pairPhone = strings.TrimPrefix(a, "--pair-phone=")
			}
		}
		t = gateway.NewWhatsAppTransport(dataDir, pairPhone)
	case "email":
		imapS := os.Getenv("GREEN_EMAIL_IMAP")
		smtpS := os.Getenv("GREEN_EMAIL_SMTP")
		user := os.Getenv("GREEN_EMAIL_USER")
		pass := os.Getenv("GREEN_EMAIL_PASSWORD")
		from := os.Getenv("GREEN_EMAIL_FROM")
		for _, a := range args {
			switch {
			case strings.HasPrefix(a, "--imap="):
				imapS = strings.TrimPrefix(a, "--imap=")
			case strings.HasPrefix(a, "--smtp="):
				smtpS = strings.TrimPrefix(a, "--smtp=")
			case strings.HasPrefix(a, "--user="):
				user = strings.TrimPrefix(a, "--user=")
			case strings.HasPrefix(a, "--password="):
				pass = strings.TrimPrefix(a, "--password=")
			case strings.HasPrefix(a, "--from="):
				from = strings.TrimPrefix(a, "--from=")
			}
		}
		if imapS == "" || user == "" || pass == "" {
			return writeExecUsageError(stderr, "email transport requires --imap, --user, --password (or GREEN_EMAIL_*)")
		}
		t = gateway.NewEmailTransport(imapS, smtpS, user, pass, from)
	case "signal":
		acct := os.Getenv("GREEN_SIGNAL_ACCOUNT")
		sock := os.Getenv("GREEN_SIGNAL_SOCKET")
		for _, a := range args {
			switch {
			case strings.HasPrefix(a, "--account="):
				acct = strings.TrimPrefix(a, "--account=")
			case strings.HasPrefix(a, "--socket="):
				sock = strings.TrimPrefix(a, "--socket=")
			}
		}
		if acct == "" {
			return writeExecUsageError(stderr, "signal transport requires --account (or GREEN_SIGNAL_ACCOUNT)")
		}
		t = gateway.NewSignalTransport(acct, sock)
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown transport %q", transport))
	}

	g := &gateway.Gateway{
		Transport: t,
		Handler:   gatewayAgentHandler(deps, stderr),
		OnError: func(msg gateway.Message, err error) {
			fmt.Fprintf(stderr, "gateway: error handling %s message from %s: %v\n", msg.Platform, msg.User, err)
		},
	}

	fmt.Fprintf(stdout, "green gateway listening on %q transport. Ctrl-C to stop.\n", t.Name())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := g.Run(ctx); err != nil && ctx.Err() == nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	fmt.Fprintln(stdout, "\ngreen gateway stopped.")
	return exitSuccess
}

// gatewayAgentHandler turns an inbound chat message into an agent run by routing
// it through the existing headless exec path (which builds the full tool/sandbox
// provider stack) and returning the captured text as the reply.
func gatewayAgentHandler(deps appDeps, stderr io.Writer) gateway.Handler {
	return gateway.HandlerFunc(func(ctx context.Context, msg gateway.Message) (string, error) {
		var buf bytes.Buffer
		code := runExec([]string{msg.Text, "--output-format", "text"}, &buf, &buf, deps)
		if code != 0 {
			return "", fmt.Errorf("agent run exited with code %d", code)
		}
		reply := strings.TrimSpace(buf.String())
		if reply == "" {
			reply = "I processed that, but produced no text reply."
		}
		return reply, nil
	})
}

func writeGatewayHelp(out io.Writer) int {
	help := `green gateway — reach the agent from chat platforms (Hermes messaging gateway)

Usage:
  green gateway list
  green gateway start [--transport=local|telegram] [--token=TOKEN]

Transports:
  local     Reads from stdin, writes replies to stdout. Offline and fully
            testable — great for development and shell piping.
  telegram  Telegram Bot API long-polling. Set --token (or GREEN_TELEGRAM_TOKEN)
            to the token from @BotFather.
  discord   Discord Gateway over websocket. Set --token (or GREEN_DISCORD_TOKEN)
            to your bot token.
  slack     Slack Socket Mode. Set --token (bot, xoxb-) and --app-token
            (app-level, xapp-) — or GREEN_SLACK_BOT_TOKEN / GREEN_SLACK_APP_TOKEN.
  whatsapp  WhatsApp via whatsmeow (native multidevice protocol). Set
            --data-dir (session db, default ~/.local/share/green/whatsmeow) and
            optionally --pair-phone (e.g. +15551234567) to pair by phone code
            instead of QR. First run prints a QR payload to scan in WhatsApp.
  email     Email via IMAP+SMTP. Set --imap/--smtp/--user/--password/--from (or
            GREEN_EMAIL_*). Polls INBOX for unseen mail, replies over SMTP.
  signal    Signal via signal-cli daemon. Set --account (or GREEN_SIGNAL_ACCOUNT)
            and optionally --socket (default 127.0.0.1:7583).

Inbound messages are handled by the same agent that powers ` + "`green exec`" + `, so
tools, skills, memory, and context files all apply.
`
	fmt.Fprint(out, help)
	return exitSuccess
}
