package cli

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/BlusceLabs/green/internal/budget"
	"github.com/BlusceLabs/green/internal/config"
)

// loadBudgetTracker builds a token-budget tracker backed by the user config
// dir. A missing/errant config dir degrades to a non-persistent (in-memory)
// tracker rather than failing the run. GREEN_TOKEN_BUDGET seeds the daily limit
// when no explicit limit has been set.
func loadBudgetTracker() *budget.Tracker {
	dir, err := config.UserConfigDir()
	var t *budget.Tracker
	if err != nil {
		t, _ = budget.New("")
	} else if t, err = budget.New(dir); err != nil {
		t, _ = budget.New("")
	}
	if v := os.Getenv("GREEN_TOKEN_BUDGET"); v != "" {
		if n, e := strconv.Atoi(strings.TrimSpace(v)); e == nil && n > 0 && t.Limit() == 0 {
			_ = t.SetLimit(n)
		}
	}
	return t
}

func runBudget(args []string, stdout io.Writer, stderr io.Writer) int {
	command := "status"
	rest := args
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			return writeBudgetHelp(stdout)
		case "status", "set", "reset", "override":
			command, rest = args[0], args[1:]
		default:
			return writeExecUsageError(stderr, fmt.Sprintf("unknown budget subcommand %q", args[0]))
		}
	}

	t := loadBudgetTracker()
	switch command {
	case "status":
		s := t.Status()
		if s.Limit == 0 {
			fmt.Fprintf(stdout, "Daily token budget: unlimited (tracking %d tokens today).\n", s.Used)
		} else {
			fmt.Fprintf(stdout, "Daily token budget for %s:\n", s.Date)
			fmt.Fprintf(stdout, "  used:      %d\n", s.Used)
			fmt.Fprintf(stdout, "  limit:     %d\n", s.Limit)
			fmt.Fprintf(stdout, "  remaining: %d\n", s.Remaining)
			if s.Over {
				fmt.Fprintf(stdout, "  status:    OVER LIMIT%s\n", boolLabel(s.Override, " (override active)", ""))
			} else {
				fmt.Fprintf(stdout, "  status:    ok%s\n", boolLabel(s.Override, " (override active)", ""))
			}
		}
		return exitSuccess
	case "set":
		if len(rest) == 0 {
			return writeExecUsageError(stderr, "usage: green budget set <tokens-per-day>")
		}
		n, err := strconv.Atoi(strings.TrimSpace(rest[0]))
		if err != nil || n < 0 {
			return writeExecUsageError(stderr, "limit must be a non-negative integer")
		}
		if err := t.SetLimit(n); err != nil {
			return writeAppError(stderr, err.Error(), exitCrash)
		}
		fmt.Fprintf(stdout, "Daily token budget set to %d tokens.\n", n)
		return exitSuccess
	case "reset":
		if err := t.Reset(); err != nil {
			return writeAppError(stderr, err.Error(), exitCrash)
		}
		fmt.Fprintln(stdout, "Today's token usage reset.")
		return exitSuccess
	case "override":
		on := true
		if len(rest) > 0 {
			switch strings.TrimSpace(rest[0]) {
			case "on", "true", "1":
				on = true
			case "off", "false", "0":
				on = false
			default:
				return writeExecUsageError(stderr, "usage: green budget override [on|off]")
			}
		}
		if err := t.SetOverride(on); err != nil {
			return writeAppError(stderr, err.Error(), exitCrash)
		}
		fmt.Fprintf(stdout, "Budget override %s.\n", boolLabel(on, "enabled", "disabled"))
		return exitSuccess
	}
	return exitSuccess
}

func boolLabel(b bool, trueStr, falseStr string) string {
	if b {
		return trueStr
	}
	return falseStr
}

func writeBudgetHelp(w io.Writer) int {
	_, _ = fmt.Fprint(w, `Usage:
  green budget <command>

Track and cap daily LLM token spend. Usage is recorded automatically on every
run and persisted under the user config dir.

Commands:
  status            Show today's usage and limit (default)
  set <n>           Set the daily token limit (0 = unlimited)
  reset             Reset today's usage and clear the override
  override [on|off] Allow the run to continue past the limit for this session

Environment:
  GREEN_TOKEN_BUDGET   Default daily token limit (0 = unlimited)
`)
	return exitSuccess
}
