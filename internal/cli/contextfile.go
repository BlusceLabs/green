package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/BlusceLabs/green/internal/config"
	"github.com/BlusceLabs/green/internal/contextfiles"
)

func runContextFile(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	if len(args) == 0 {
		return runContextFileList(stdout, stderr, deps)
	}
	switch args[0] {
	case "-h", "--help", "help":
		return writeContextFileHelp(stdout)
	case "list", "ls":
		return runContextFileList(stdout, stderr, deps)
	case "show", "cat":
		scope := contextfiles.ScopeProject
		if len(args) > 1 {
			scope = contextfiles.Scope(args[1])
		}
		return runContextFileShow(scope, stdout, stderr, deps)
	case "add", "append":
		scope := contextfiles.ScopeProject
		rest := args[1:]
		if len(rest) > 0 && (rest[0] == "user" || rest[0] == "project") {
			scope = contextfiles.Scope(rest[0])
			rest = rest[1:]
		}
		line := strings.Join(rest, " ")
		line = strings.TrimSpace(line)
		line = strings.Trim(line, `"'`)
		if line == "" {
			return writeExecUsageError(stderr, "contextfile add requires text")
		}
		return runContextFileAdd(scope, line, stdout, stderr, deps)
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown contextfile subcommand %q", args[0]))
	}
}

func contextFileScopes(deps appDeps) (string, string) {
	root, _ := resolveWorkspaceRoot("", deps)
	userDir, _ := config.UserConfigDir()
	return root, userDir
}

func runContextFileList(stdout io.Writer, stderr io.Writer, deps appDeps) int {
	root, userDir := contextFileScopes(deps)
	files, err := contextfiles.Load(root, userDir)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if len(files) == 0 {
		fmt.Fprintln(stdout, "No context files. Create one with `green contextfile add \"...\"`.")
		return exitSuccess
	}
	for _, f := range files {
		fmt.Fprintf(stdout, "[%s] %s (%d bytes)\n", f.Scope, f.Path, len(f.Content))
	}
	return exitSuccess
}

func runContextFileShow(scope contextfiles.Scope, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	root, userDir := contextFileScopes(deps)
	files, err := contextfiles.Load(root, userDir)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	for _, f := range files {
		if f.Scope == scope {
			fmt.Fprintf(stdout, "# %s context (%s)\n\n%s\n", scope, f.Path, f.Content)
			return exitSuccess
		}
	}
	fmt.Fprintf(stdout, "No %s context file found.\n", scope)
	return exitSuccess
}

func runContextFileAdd(scope contextfiles.Scope, line string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	root, userDir := contextFileScopes(deps)
	path, err := contextfiles.Append(scope, root, userDir, line)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	fmt.Fprintf(stdout, "Appended to %s context: %s\n", scope, path)
	return exitSuccess
}

func writeContextFileHelp(out io.Writer) int {
	help := `green contextfile — durable, evolving project context (Hermes-style)

Usage:
  green contextfile list
  green contextfile show [project|user]
  green contextfile add [project|user] "a line of context"

Context is softer than AGENTS.md rules: running notes, decisions, and gotchas
that resurface in future sessions. Project context lives in <root>/CONTEXT.md
(shared with the team); user context lives in the user config dir and follows
you across projects. Both are merged into the system prompt each run.
`
	fmt.Fprint(out, help)
	return exitSuccess
}
