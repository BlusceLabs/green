package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/BlusceLabs/green/internal/learning"
	"github.com/BlusceLabs/green/internal/redaction"
	"github.com/BlusceLabs/green/internal/sessions"
)

func runLearn(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	command := "memory"
	rest := args
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			return writeLearnHelp(stdout)
		case "memory", "profile", "reflect", "nudge", "skills", "interview", "enable-autoreflect":
			command, rest = args[0], args[1:]
		default:
			if !strings.HasPrefix(args[0], "-") {
				return writeExecUsageError(stderr, fmt.Sprintf("unknown learn subcommand %q", args[0]))
			}
		}
	}
	switch command {
	case "memory":
		return runLearnMemory(rest, stdout, stderr)
	case "profile":
		return runLearnProfile(rest, stdout, stderr)
	case "reflect":
		return runLearnReflect(rest, stdout, stderr, deps)
	case "nudge":
		return runLearnNudge(rest, stdout, stderr)
	case "skills":
		return runLearnSkills(rest, stdout, stderr, deps)
	case "interview":
		return runLearnInterview(rest, stdout, stderr)
	case "enable-autoreflect":
		return runLearnEnableAutoReflect(stdout, stderr, deps)
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown learn subcommand %q", command))
	}
}

func learnStore() *learning.Store {
	return learning.NewStore(learning.DefaultDir(nil))
}

func runLearnMemory(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		args = []string{"list"}
	}
	switch args[0] {
	case "list", "ls":
		cat := ""
		asJSON := false
		for _, a := range args[1:] {
			switch {
			case a == "--json":
				asJSON = true
			case strings.HasPrefix(a, "--category="):
				cat = strings.TrimPrefix(a, "--category=")
			case strings.HasPrefix(a, "-"):
				return writeExecUsageError(stderr, fmt.Sprintf("unknown memory flag %q", a))
			}
		}
		store := learnStore()
		memories, err := store.ListMemories(learning.MemoryCategory(cat))
		if err != nil {
			return writeAppError(stderr, err.Error(), exitCrash)
		}
		if asJSON {
			if err := writePrettyJSON(stdout, memories); err != nil {
				return writeAppError(stderr, err.Error(), exitCrash)
			}
			return exitSuccess
		}
		if len(memories) == 0 {
			fmt.Fprintln(stdout, "No memories yet. Run `green learn reflect <session>` or `green learn memory add \"...\"`.")
			return exitSuccess
		}
		for _, m := range memories {
			fmt.Fprintf(stdout, "[%s] %s (%s) — %s\n", m.ID, m.Category, m.Source, m.Content)
		}
		return exitSuccess
	case "add":
		var category, source string
		text := strings.Join(args[1:], " ")
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			switch {
			case rest[i] == "--category" && i+1 < len(rest):
				category = rest[i+1]
				i++
			case strings.HasPrefix(rest[i], "--category="):
				category = strings.TrimPrefix(rest[i], "--category=")
			case rest[i] == "--source" && i+1 < len(rest):
				source = rest[i+1]
				i++
			case strings.HasPrefix(rest[i], "--source="):
				source = strings.TrimPrefix(rest[i], "--source=")
			}
		}
		text = stripFlagPrefixes(text)
		if strings.TrimSpace(text) == "" {
			return writeExecUsageError(stderr, "memory add requires text")
		}
		store := learnStore()
		if err := store.Ensure(); err != nil {
			return writeAppError(stderr, err.Error(), exitCrash)
		}
		e, err := store.AppendMemory("", text, learning.MemoryCategory(category), source)
		if err != nil {
			return writeAppError(stderr, err.Error(), exitCrash)
		}
		fmt.Fprintf(stdout, "Added memory %s\n", e.ID)
		return exitSuccess
	case "remove", "rm":
		if len(args) < 2 {
			return writeExecUsageError(stderr, "memory remove requires an id")
		}
		store := learnStore()
		ok, err := store.RemoveMemory(args[1])
		if err != nil {
			return writeAppError(stderr, err.Error(), exitCrash)
		}
		if !ok {
			return writeExecUsageError(stderr, fmt.Sprintf("memory %q not found", args[1]))
		}
		fmt.Fprintln(stdout, "Removed.")
		return exitSuccess
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown memory subcommand %q", args[0]))
	}
}

func runLearnProfile(args []string, stdout io.Writer, stderr io.Writer) int {
	store := learnStore()
	if len(args) == 0 || args[0] == "show" {
		p, err := store.GetProfile()
		if err != nil {
			return writeAppError(stderr, err.Error(), exitCrash)
		}
		if len(p.Facts) == 0 && len(p.Preferences) == 0 && len(p.Topics) == 0 {
			fmt.Fprintln(stdout, "No user profile yet.")
			return exitSuccess
		}
		fmt.Fprintf(stdout, "Facts: %s\n", strings.Join(p.Facts, "; "))
		fmt.Fprintf(stdout, "Preferences: %s\n", strings.Join(p.Preferences, "; "))
		fmt.Fprintf(stdout, "Topics: %s\n", strings.Join(p.Topics, "; "))
		return exitSuccess
	}
	if args[0] == "update" {
		var facts, prefs, topics []string
		for i := 1; i < len(args); i++ {
			switch {
			case strings.HasPrefix(args[i], "--fact="):
				facts = append(facts, strings.TrimPrefix(args[i], "--fact="))
			case strings.HasPrefix(args[i], "--pref="):
				prefs = append(prefs, strings.TrimPrefix(args[i], "--pref="))
			case strings.HasPrefix(args[i], "--topic="):
				topics = append(topics, strings.TrimPrefix(args[i], "--topic="))
			}
		}
		if err := store.Ensure(); err != nil {
			return writeAppError(stderr, err.Error(), exitCrash)
		}
		p, err := store.UpdateProfile(facts, prefs, topics)
		if err != nil {
			return writeAppError(stderr, err.Error(), exitCrash)
		}
		fmt.Fprintf(stdout, "Profile updated: %d facts, %d preferences, %d topics\n", len(p.Facts), len(p.Preferences), len(p.Topics))
		return exitSuccess
	}
	return writeExecUsageError(stderr, fmt.Sprintf("unknown profile subcommand %q", args[0]))
}

func runLearnReflect(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	useLLM := false
	rest := args
	for i := 0; i < len(rest); i++ {
		if rest[i] == "--llm" {
			useLLM = true
			rest = append(rest[:i], rest[i+1:]...)
			i--
		}
	}
	if len(rest) < 1 {
		return writeExecUsageError(stderr, "reflect requires a session id")
	}
	sessionID := rest[0]
	store := sessions.NewStore(sessions.StoreOptions{})
	meta, err := store.Get(sessionID)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if meta == nil {
		return writeExecUsageError(stderr, fmt.Sprintf("session not found: %s", sessionID))
	}
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	transcript := learning.Transcript{SessionID: sessionID}
	for _, ev := range events {
		turn := learning.Turn{Role: string(ev.Type), Content: extractTurnText(ev.Payload)}
		transcript.Turns = append(transcript.Turns, turn)
	}
	ls := learnStore()
	if err := ls.Ensure(); err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if useLLM {
		provider, perr := resolveActiveProvider(deps)
		if perr != nil {
			return writeAppError(stderr, perr.Error(), exitCrash)
		}
		ls.Summarizer = providerSummarizer(provider)
	}
	rep, err := ls.Reflect(transcript)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	out, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Fprintln(stdout, string(out))
	return exitSuccess
}

func runLearnNudge(args []string, stdout io.Writer, stderr io.Writer) int {
	store := learnStore()
	if len(args) == 0 || args[0] == "status" {
		status, err := store.Nudge()
		if err != nil {
			return writeAppError(stderr, err.Error(), exitCrash)
		}
		out, _ := json.MarshalIndent(status, "", "  ")
		fmt.Fprintln(stdout, string(out))
		return exitSuccess
	}
	if args[0] == "ack" || args[0] == "acknowledge" {
		if err := store.AcknowledgeNudge(); err != nil {
			return writeAppError(stderr, err.Error(), exitCrash)
		}
		fmt.Fprintln(stdout, "Nudge acknowledged; knowledge checkpoint recorded.")
		return exitSuccess
	}
	return writeExecUsageError(stderr, fmt.Sprintf("unknown nudge subcommand %q", args[0]))
}

func runLearnSkills(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	if len(args) == 0 {
		return writeExecUsageError(stderr, "usage: green learn skills create-from|export|import ...")
	}
	switch args[0] {
	case "create-from":
		return runLearnSkillsCreate(args[1:], stdout, stderr, deps)
	case "export":
		return runLearnSkillsExport(args[1:], stdout, stderr)
	case "import":
		return runLearnSkillsImport(args[1:], stdout, stderr)
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown skills subcommand %q", args[0]))
	}
}

// runLearnSkillsCreate mints a reusable skill from a completed session.
func runLearnSkillsCreate(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	useLLM := false
	rest := args
	for i := 0; i < len(rest); i++ {
		if rest[i] == "--llm" {
			useLLM = true
			rest = append(rest[:i], rest[i+1:]...)
			i--
		}
	}
	if len(rest) < 1 {
		return writeExecUsageError(stderr, "create-from requires a session id")
	}
	sessionID := rest[0]
	store := sessions.NewStore(sessions.StoreOptions{})
	meta, err := store.Get(sessionID)
	if err != nil || meta == nil {
		return writeExecUsageError(stderr, fmt.Sprintf("session not found: %s", sessionID))
	}
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	transcript := learning.Transcript{SessionID: sessionID}
	for _, ev := range events {
		transcript.Turns = append(transcript.Turns, learning.Turn{Role: string(ev.Type), Content: extractTurnText(ev.Payload)})
	}
	ls := learnStore()
	if err := ls.Ensure(); err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if useLLM {
		provider, perr := resolveActiveProvider(deps)
		if perr != nil {
			return writeAppError(stderr, perr.Error(), exitCrash)
		}
		ls.Summarizer = providerSummarizer(provider)
	}
	draft, ok, err := ls.ExtractSkill(transcript, ls.Summarizer)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if !ok {
		fmt.Fprintln(stderr, "No skill-worthy task detected in that session.")
		return exitSuccess
	}
	path, err := ls.WriteSkill(draft)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	fmt.Fprintf(stdout, "Wrote skill %q to %s\n", draft.Name, path)
	return exitSuccess
}

func extractTurnText(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		return redaction.RedactString(string(payload), redaction.Options{})
	}
	for _, key := range []string{"content", "text", "arguments", "output", "input"} {
		if v, ok := m[key].(string); ok {
			return v
		}
	}
	return redaction.RedactString(string(payload), redaction.Options{})
}

func stripFlagPrefixes(text string) string {
	fields := strings.Fields(text)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if strings.HasPrefix(f, "--category=") || strings.HasPrefix(f, "--source=") ||
			f == "--category" || f == "--source" {
			continue
		}
		out = append(out, f)
	}
	return strings.Join(out, " ")
}

func writeLearnHelp(out io.Writer) int {
	help := `green learn — the self-improving learning loop

Usage:
  green learn memory list [--json] [--category=CAT]
  green learn memory add "text" [--category=preference|fact|procedure|feedback|project] [--source=SRC]
  green learn memory remove <id>
  green learn profile show
  green learn profile update [--fact="..."] [--pref="..."] [--topic="..."]
  green learn reflect <session-id> [--llm]
  green learn nudge status | ack
  green learn skills create-from <session-id> [--llm]
  green learn skills export [--dir=DIR]
  green learn skills import <dir>
  green learn interview
  green learn enable-autoreflect

The learning loop persists memory and a user profile across sessions, and can
mint reusable skills from completed tasks. State lives under the learning dir
(override with GREEN_LEARNING_DIR). Pass --llm to reflect / skills create-from to
upgrade extraction and skill authoring with the active model.
`
	fmt.Fprint(out, help)
	return exitSuccess
}

// runLearnInterview runs a short dialectic to deepen the user profile: it
// surfaces the gaps in the current profile, asks targeted questions, and folds
// the answers back in. It reads answers one per line from stdin (non-interactive
// friendly: pipe answers in). This is the lightweight, local version of Hermes's
// Honcho-style dialectic user modeling.
func runLearnInterview(args []string, stdout io.Writer, stderr io.Writer) int {
	store := learnStore()
	if err := store.Ensure(); err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	profile, err := store.GetProfile()
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}

	questions := interviewQuestions(profile)
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		return writeLearnInterviewHelp(stdout)
	}

	var facts, prefs, topics []string
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for i, q := range questions {
		fmt.Fprintf(stdout, "Q%d. %s\n> ", i+1, q)
		if !scanner.Scan() {
			break
		}
		answer := strings.TrimSpace(scanner.Text())
		if answer == "" {
			continue
		}
		switch {
		case strings.Contains(strings.ToLower(q), "preference"):
			prefs = append(prefs, answer)
		case strings.Contains(strings.ToLower(q), "interested") || strings.Contains(strings.ToLower(q), "working on"):
			topics = append(topics, answer)
		default:
			facts = append(facts, answer)
		}
	}
	if len(facts) == 0 && len(prefs) == 0 && len(topics) == 0 {
		fmt.Fprintln(stdout, "No answers captured; profile unchanged.")
		return exitSuccess
	}
	updated, err := store.UpdateProfile(facts, prefs, topics)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	fmt.Fprintf(stdout, "Profile deepened: %d facts, %d preferences, %d topics.\n",
		len(updated.Facts), len(updated.Preferences), len(updated.Topics))
	return exitSuccess
}

// interviewQuestions proposes up to three questions targeting gaps in the
// profile, so each interview tightens the model of the user.
func interviewQuestions(p learning.Profile) []string {
	qs := []string{}
	if len(p.Facts) == 0 {
		qs = append(qs, "What do you do, and what kind of work should I assume you're usually doing?")
	} else {
		qs = append(qs, "Anything about your role or working style I should remember for next time?")
	}
	if len(p.Preferences) == 0 {
		qs = append(qs, "Do you have a preference for how I communicate or structure my work (e.g. concise, explanatory, step-by-step)?")
	} else {
		qs = append(qs, "Any new preferences since we last talked?")
	}
	if len(p.Topics) == 0 {
		qs = append(qs, "What are you most interested in or working on right now?")
	} else {
		qs = append(qs, "Any new topics or projects I should keep in mind?")
	}
	if len(qs) > 3 {
		qs = qs[:3]
	}
	return qs
}

func writeLearnInterviewHelp(out io.Writer) int {
	help := `green learn interview — deepen the user profile (dialectic)

Usage:
  green learn interview

Asks a few questions based on gaps in the current profile and folds the answers
back into the profile. Answers are read one per line from stdin.
`
	fmt.Fprint(out, help)
	return exitSuccess
}

// runLearnSkillsExport bundles all auto-created skills (the learning SkillsDir)
// into a target directory in agentskills.io-compatible form (each skill is a
// SKILL.md under its own folder). `green learn skills export --dir DIR`.
func runLearnSkillsExport(args []string, stdout io.Writer, stderr io.Writer) int {
	dir := "green-skills"
	for _, a := range args {
		if strings.HasPrefix(a, "--dir=") {
			dir = strings.TrimPrefix(a, "--dir=")
		}
	}
	src := learning.NewStore(learning.DefaultDir(nil)).SkillsDir
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(stdout, "No skills to export yet.")
			return exitSuccess
		}
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	copied := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		srcFile := filepath.Join(src, e.Name(), "SKILL.md")
		data, err := os.ReadFile(srcFile)
		if err != nil {
			continue
		}
		dstDir := filepath.Join(dir, e.Name())
		if err := os.MkdirAll(dstDir, 0o700); err != nil {
			return writeAppError(stderr, err.Error(), exitCrash)
		}
		if err := os.WriteFile(filepath.Join(dstDir, "SKILL.md"), data, 0o600); err != nil {
			return writeAppError(stderr, err.Error(), exitCrash)
		}
		copied++
	}
	fmt.Fprintf(stdout, "Exported %d skill(s) to %s (agentskills.io-compatible).\n", copied, dir)
	return exitSuccess
}

// runLearnSkillsImport copies agentskills.io-compatible skills from a directory
// into the learning SkillsDir so they become available to the agent.
func runLearnSkillsImport(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) < 1 {
		return writeExecUsageError(stderr, "import requires a source directory")
	}
	src := args[0]
	entries, err := os.ReadDir(src)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	ls := learning.NewStore(learning.DefaultDir(nil))
	if err := ls.Ensure(); err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	copied := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		srcFile := filepath.Join(src, e.Name(), "SKILL.md")
		data, err := os.ReadFile(srcFile)
		if err != nil {
			continue
		}
		dstDir := filepath.Join(ls.SkillsDir, e.Name())
		if err := os.MkdirAll(dstDir, 0o700); err != nil {
			return writeAppError(stderr, err.Error(), exitCrash)
		}
		if err := os.WriteFile(filepath.Join(dstDir, "SKILL.md"), data, 0o600); err != nil {
			return writeAppError(stderr, err.Error(), exitCrash)
		}
		copied++
	}
	fmt.Fprintf(stdout, "Imported %d skill(s) into the learning skills dir.\n", copied)
	return exitSuccess
}

// reflectHookScript is the sessionEnd hook body written by enable-autoreflect.
// It mirrors scripts/green-learn-reflect.sh; the command materializes it into
// the project's .green/hooks so it is self-contained.
const reflectHookScript = `#!/usr/bin/env bash
set -euo pipefail
payload="$(cat)"
session_id="$(printf '%s' "$payload" | grep -o '"sessionId"[^,}]*' | head -1 | sed -E 's/.*:[[:space:]]*"([^"]+)".*/\1/')"
if [ -z "$session_id" ]; then
  session_id="$(printf '%s' "$payload" | grep -o '"sessionID"[^,}]*' | head -1 | sed -E 's/.*:[[:space:]]*"([^"]+)".*/\1/')"
fi
[ -z "$session_id" ] && exit 0
green learn reflect "$session_id" >/dev/null 2>&1 || true
`

// runLearnEnableAutoReflect installs a sessionEnd hook that runs the learning
// loop's reflect step after every session, so memory/skills accrue without a
// manual command. The hook is written under <root>/.green/hooks and enabled in
// <root>/.green/hooks.json (project scope).
func runLearnEnableAutoReflect(stdout io.Writer, stderr io.Writer, deps appDeps) int {
	root, err := resolveWorkspaceRoot("", deps)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	hooksDir := filepath.Join(root, ".green", "hooks")
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	scriptPath := filepath.Join(hooksDir, "green-learn-reflect.sh")
	if err := os.WriteFile(scriptPath, []byte(reflectHookScript), 0o700); err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	hooksPath := filepath.Join(root, ".green", "hooks.json")
	manifest := map[string]any{
		"enabled": true,
		"hooks": []map[string]any{
			{
				"id":      "green-learn-reflect",
				"event":   "sessionEnd",
				"command": scriptPath,
				"enabled": true,
			},
		},
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if err := os.WriteFile(hooksPath, append(data, '\n'), 0o600); err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	fmt.Fprintf(stdout, "Auto-reflect enabled. After each session, `green learn reflect` runs via %s.\n", hooksPath)
	return exitSuccess
}
