package cli

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/BlusceLabs/green/internal/kanban"
)

func runKanban(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	command := "list"
	rest := args
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			return writeKanbanHelp(stdout)
		case "list", "create", "show", "add", "status", "comment", "remove", "rm-card", "run":
			command, rest = args[0], args[1:]
		default:
			return writeExecUsageError(stderr, fmt.Sprintf("unknown kanban subcommand %q", args[0]))
		}
	}

	store, err := kanban.Open(kanban.DefaultConfigDir())
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}

	switch command {
	case "list":
		return kanbanList(store, stdout, stderr)
	case "create":
		if len(rest) == 0 {
			return writeExecUsageError(stderr, "usage: green kanban create <name>")
		}
		b, err := store.CreateBoard(strings.Join(rest, " "))
		if err != nil {
			return writeAppError(stderr, err.Error(), exitCrash)
		}
		fmt.Fprintf(stdout, "Created board %q (%s).\n", b.Name, b.ID)
		return exitSuccess
	case "show":
		if len(rest) == 0 {
			return writeExecUsageError(stderr, "usage: green kanban show <board>")
		}
		b, err := store.GetBoard(rest[0])
		if err != nil {
			return writeAppError(stderr, err.Error(), exitUsage)
		}
		return kanbanShow(b, stdout)
	case "add":
		return kanbanAdd(store, rest, stdout, stderr)
	case "status":
		return kanbanSetStatus(store, rest, stdout, stderr)
	case "comment":
		if len(rest) < 2 {
			return writeExecUsageError(stderr, "usage: green kanban comment <card-id> <text>")
		}
		c, err := store.AddComment(rest[0], "user", strings.Join(rest[1:], " "))
		if err != nil {
			return writeAppError(stderr, err.Error(), exitUsage)
		}
		fmt.Fprintf(stdout, "Commented on card %s (%s).\n", c.ID, c.Status)
		return exitSuccess
	case "remove":
		if len(rest) == 0 {
			return writeExecUsageError(stderr, "usage: green kanban remove <board>")
		}
		if err := store.RemoveBoard(rest[0]); err != nil {
			return writeAppError(stderr, err.Error(), exitUsage)
		}
		fmt.Fprintf(stdout, "Removed board %q.\n", rest[0])
		return exitSuccess
	case "rm-card":
		if len(rest) == 0 {
			return writeExecUsageError(stderr, "usage: green kanban rm-card <card-id>")
		}
		if err := store.RemoveCard(rest[0]); err != nil {
			return writeAppError(stderr, err.Error(), exitUsage)
		}
		fmt.Fprintf(stdout, "Removed card %q.\n", rest[0])
		return exitSuccess
	case "run":
		return kanbanRun(store, rest, stdout, stderr, deps)
	}
	return exitSuccess
}

func kanbanList(store *kanban.Store, stdout, stderr io.Writer) int {
	boards, err := store.ListBoards()
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if len(boards) == 0 {
		fmt.Fprintln(stdout, "No kanban boards. Create one with: green kanban create <name>")
		return exitSuccess
	}
	for _, b := range boards {
		todo, doing, done, blocked := 0, 0, 0, 0
		for _, c := range b.Cards {
			switch c.Status {
			case kanban.StatusTodo:
				todo++
			case kanban.StatusDoing:
				doing++
			case kanban.StatusDone:
				done++
			case kanban.StatusBlocked:
				blocked++
			}
		}
		fmt.Fprintf(stdout, "%s  %s  [todo:%d doing:%d done:%d blocked:%d]\n", b.ID, b.Name, todo, doing, done, blocked)
	}
	return exitSuccess
}

func kanbanShow(b *kanban.Board, stdout io.Writer) int {
	fmt.Fprintf(stdout, "Board: %s (%s)\n", b.Name, b.ID)
	if len(b.Cards) == 0 {
		fmt.Fprintln(stdout, "  (no cards)")
		return exitSuccess
	}
	order := map[kanban.Status]int{kanban.StatusBlocked: 0, kanban.StatusDoing: 1, kanban.StatusTodo: 2, kanban.StatusDone: 3}
	sorted := append([]kanban.Card{}, b.Cards...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if order[sorted[i].Status] != order[sorted[j].Status] {
			return order[sorted[i].Status] < order[sorted[j].Status]
		}
		return sorted[i].Priority > sorted[j].Priority
	})
	for _, c := range sorted {
		fmt.Fprintf(stdout, "  [%s] %s  %s  pri:%s  labels:%s\n", c.Status, c.ID, c.Title, c.Priority, strings.Join(c.Labels, ","))
		if c.Description != "" {
			fmt.Fprintf(stdout, "      %s\n", c.Description)
		}
	}
	return exitSuccess
}

func kanbanAdd(store *kanban.Store, rest []string, stdout, stderr io.Writer) int {
	if len(rest) == 0 {
		return writeExecUsageError(stderr, "usage: green kanban add <board> <title> [--desc <text>] [--priority low|med|high] [--label <l>]... [--depends <card-id>]...")
	}
	boardRef := rest[0]
	titleParts := []string{}
	var description, priority string
	var labels, deps []string
	i := 1
	for i < len(rest) {
		switch rest[i] {
		case "--desc":
			i++
			if i < len(rest) {
				description = rest[i]
			}
		case "--priority":
			i++
			if i < len(rest) {
				priority = rest[i]
			}
		case "--label":
			i++
			if i < len(rest) {
				labels = append(labels, rest[i])
			}
		case "--depends":
			i++
			if i < len(rest) {
				deps = append(deps, rest[i])
			}
		default:
			titleParts = append(titleParts, rest[i])
		}
		i++
	}
	if len(titleParts) == 0 {
		return writeExecUsageError(stderr, "a card title is required")
	}
	if priority != "" && priority != "low" && priority != "med" && priority != "high" {
		return writeExecUsageError(stderr, "--priority must be low, med, or high")
	}
	c, err := store.AddCard(boardRef, strings.Join(titleParts, " "), description, kanban.Priority(priority), labels, deps)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitUsage)
	}
	fmt.Fprintf(stdout, "Added card %q to board %q (%s).\n", c.Title, boardRef, c.ID)
	return exitSuccess
}

func kanbanSetStatus(store *kanban.Store, rest []string, stdout, stderr io.Writer) int {
	if len(rest) < 2 {
		return writeExecUsageError(stderr, "usage: green kanban status <card-id> <todo|doing|done|blocked>")
	}
	if !kanban.ValidStatus(rest[1]) {
		return writeExecUsageError(stderr, "status must be todo, doing, done, or blocked")
	}
	c, err := store.SetStatus(rest[0], kanban.Status(rest[1]))
	if err != nil {
		return writeAppError(stderr, err.Error(), exitUsage)
	}
	fmt.Fprintf(stdout, "Card %s is now %s.\n", c.ID, c.Status)
	return exitSuccess
}

// kanbanRun processes board cards through the agent. Each eligible card (todo or
// doing, with satisfied dependencies) becomes a prompt executed via the normal
// exec path; on success the card is marked done and the result stored as a
// comment, on failure it is marked blocked.
func kanbanRun(store *kanban.Store, rest []string, stdout, stderr io.Writer, deps appDeps) int {
	if len(rest) == 0 {
		return writeExecUsageError(stderr, "usage: green kanban run <board> [--card <id>]")
	}
	boardRef := rest[0]
	onlyCard := ""
	for i := 1; i < len(rest); i++ {
		if rest[i] == "--card" && i+1 < len(rest) {
			onlyCard = rest[i+1]
			i++
		}
	}
	b, err := store.GetBoard(boardRef)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitUsage)
	}
	cards := b.Cards
	if onlyCard != "" {
		found := false
		for _, c := range b.Cards {
			if c.ID == onlyCard {
				cards = []kanban.Card{c}
				found = true
				break
			}
		}
		if !found {
			return writeAppError(stderr, fmt.Sprintf("card %q not found on board", onlyCard), exitUsage)
		}
	}

	processed := 0
	for _, c := range cards {
		if onlyCard == "" && c.Status != kanban.StatusTodo && c.Status != kanban.StatusDoing {
			continue
		}
		if !depsSatisfied(c, b) {
			if onlyCard == c.ID {
				fmt.Fprintf(stderr, "card %s has unsatisfied dependencies; skipping.\n", c.ID)
			}
			continue
		}
		prompt := buildCardPrompt(c, b)
		fmt.Fprintf(stdout, "Running card %s (%s)...\n", c.ID, c.Title)
		var out bytes.Buffer
		code := runExec([]string{"--prompt", prompt}, &out, stderr, deps)
		if code == 0 {
			_, _ = store.SetStatus(c.ID, kanban.StatusDone)
			_, _ = store.AddComment(c.ID, "agent", truncateForComment(out.String()))
			fmt.Fprintf(stdout, "  done.\n")
		} else {
			_, _ = store.SetStatus(c.ID, kanban.StatusBlocked)
			_, _ = store.AddComment(c.ID, "agent", "run failed (exit "+itoa(code)+"):\n"+truncateForComment(out.String()))
			fmt.Fprintf(stdout, "  blocked (run failed).\n")
		}
		processed++
	}
	if processed == 0 {
		fmt.Fprintln(stdout, "Nothing to run (no eligible cards).")
	}
	return exitSuccess
}

func depsSatisfied(c kanban.Card, b *kanban.Board) bool {
	if len(c.Dependencies) == 0 {
		return true
	}
	done := map[string]bool{}
	for _, other := range b.Cards {
		if other.Status == kanban.StatusDone {
			done[other.ID] = true
		}
	}
	for _, d := range c.Dependencies {
		if !done[d] {
			return false
		}
	}
	return true
}

func buildCardPrompt(c kanban.Card, b *kanban.Board) string {
	var sb strings.Builder
	sb.WriteString("Work the following task from the kanban board \"" + b.Name + "\".\n\n")
	sb.WriteString("Title: " + c.Title + "\n")
	if c.Description != "" {
		sb.WriteString("Description: " + c.Description + "\n")
	}
	if len(c.Labels) > 0 {
		sb.WriteString("Labels: " + strings.Join(c.Labels, ", ") + "\n")
	}
	if len(c.Dependencies) > 0 {
		sb.WriteString("Depends on: " + strings.Join(c.Dependencies, ", ") + "\n")
	}
	sb.WriteString("\nComplete the task, make any code changes needed, and report a concise summary of what you did.")
	return sb.String()
}

func truncateForComment(s string) string {
	const max = 4000
	if len(s) <= max {
		return s
	}
	return "...(truncated)...\n" + s[len(s)-max:]
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

func writeKanbanHelp(w io.Writer) int {
	_, _ = fmt.Fprint(w, `Usage:
  green kanban <command>

Persistent task boards the agent can execute card-by-card.

Commands:
  list                       List boards and their card counts
  create <name>              Create a board
  show <board>               Show a board's cards
  add <board> <title>        Add a card [--desc <text>] [--priority low|med|high]
                             [--label <l>]... [--depends <card-id>]...
  status <card> <status>     Set a card's status (todo|doing|done|blocked)
  comment <card> <text>      Add a comment to a card
  remove <board>             Delete a board
  rm-card <card>             Delete a card
  run <board> [--card <id>]  Execute eligible cards through the agent

Storage: ~/.config/green/kanban.json
`)
	return exitSuccess
}
