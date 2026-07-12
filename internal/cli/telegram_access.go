package cli

import (
	"fmt"
	"io"

	"github.com/BlusceLabs/green/internal/gateway"
)

func runTelegram(args []string, stdout io.Writer, stderr io.Writer) int {
	command := "list"
	rest := args
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			return writeTelegramHelp(stdout)
		case "list", "approve", "reject", "remove", "promote", "demote", "reset":
			command, rest = args[0], args[1:]
		default:
			return writeExecUsageError(stderr, fmt.Sprintf("unknown telegram subcommand %q", args[0]))
		}
	}

	store, err := gateway.LoadTelegramAccess(gateway.DefaultTelegramAccessDir())
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}

	switch command {
	case "list":
		users, pending := store.List()
		if len(users) == 0 && len(pending) == 0 {
			fmt.Fprintln(stdout, "No Telegram users. A user sends /start to the bot to request access.")
			return exitSuccess
		}
		for _, u := range users {
			fmt.Fprintf(stdout, "[%s] %s (%s)\n", u.Role, u.Username, u.ID)
		}
		for _, p := range pending {
			fmt.Fprintf(stdout, "[pending] %s (%s) code:%s\n", p.Username, p.ID, p.Code)
		}
		return exitSuccess
	case "approve":
		if len(rest) == 0 {
			return writeExecUsageError(stderr, "usage: green telegram approve <code|id>")
		}
		if err := store.Approve(rest[0]); err != nil {
			return writeAppError(stderr, err.Error(), exitUsage)
		}
		fmt.Fprintf(stdout, "Approved %q.\n", rest[0])
		return exitSuccess
	case "reject":
		if len(rest) == 0 {
			return writeExecUsageError(stderr, "usage: green telegram reject <id>")
		}
		if err := store.Reject(rest[0]); err != nil {
			return writeAppError(stderr, err.Error(), exitUsage)
		}
		fmt.Fprintf(stdout, "Rejected %q.\n", rest[0])
		return exitSuccess
	case "remove":
		if len(rest) == 0 {
			return writeExecUsageError(stderr, "usage: green telegram remove <id|username>")
		}
		if err := store.Remove(rest[0]); err != nil {
			return writeAppError(stderr, err.Error(), exitUsage)
		}
		fmt.Fprintf(stdout, "Removed %q.\n", rest[0])
		return exitSuccess
	case "promote":
		if len(rest) == 0 {
			return writeExecUsageError(stderr, "usage: green telegram promote <id|username>")
		}
		if err := store.Promote(rest[0]); err != nil {
			return writeAppError(stderr, err.Error(), exitUsage)
		}
		fmt.Fprintf(stdout, "Promoted %q to admin.\n", rest[0])
		return exitSuccess
	case "demote":
		if len(rest) == 0 {
			return writeExecUsageError(stderr, "usage: green telegram demote <id|username>")
		}
		if err := store.Demote(rest[0]); err != nil {
			return writeAppError(stderr, err.Error(), exitUsage)
		}
		fmt.Fprintf(stdout, "Demoted %q to member.\n", rest[0])
		return exitSuccess
	case "reset":
		if err := store.Reset(); err != nil {
			return writeAppError(stderr, err.Error(), exitCrash)
		}
		fmt.Fprintln(stdout, "Cleared all Telegram access.")
		return exitSuccess
	}
	return exitSuccess
}

func writeTelegramHelp(w io.Writer) int {
	_, _ = fmt.Fprint(w, `Usage:
  green telegram <command>

Manage Telegram multi-user access (admin/member roles + pairing approval).
Users send /start to the bot to request access; an admin approves the pairing
code from here.

Commands:
  list                       List approved users and pending requests
  approve <code|id>          Approve a pending request (first user becomes admin)
  reject <id>                Reject a pending request
  remove <id|username>       Revoke an approved user
  promote <id|username>      Make a member an admin
  demote <id|username>       Make an admin a member
  reset                      Clear all Telegram access
`)
	return exitSuccess
}
