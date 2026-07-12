package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BlusceLabs/green/internal/agent"
	"github.com/BlusceLabs/green/internal/lsp"
)

// runLSP exposes green's Language Server Protocol client from the command line
// (opencode's "LSP enabled for the LLM" surface): surface diagnostics, jump to
// definitions, and search workspace symbols without opening an editor. The
// agent already uses these servers during runs; this makes them scriptable.
func runLSP(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	command := "diagnostics"
	rest := args
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			return writeLSPHelp(stdout)
		case "diagnostics", "check", "goto", "definition", "find", "symbols", "references":
			command, rest = args[0], args[1:]
		default:
			if !strings.HasPrefix(args[0], "-") {
				return writeExecUsageError(stderr, fmt.Sprintf("unknown lsp subcommand %q", args[0]))
			}
		}
	}
	wd, err := deps.getwd()
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	root := wd
	if gr := agent.FindProjectGitRoot(wd); gr != "" {
		root = gr
	}
	m := lsp.NewManager(root)
	defer m.Shutdown(context.Background())

	switch command {
	case "diagnostics", "check":
		return runLSPDiagnostics(m, rest, stdout, stderr)
	case "goto", "definition":
		return runLSPGoto(m, rest, stdout, stderr)
	case "find", "symbols":
		return runLSPFind(m, rest, stdout, stderr)
	case "references":
		return runLSPReferences(m, rest, stdout, stderr)
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown lsp subcommand %q", command))
	}
}

func runLSPDiagnostics(m *lsp.Manager, args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		return writeExecUsageError(stderr, "usage: green lsp diagnostics <file>")
	}
	path := args[0]
	text, err := os.ReadFile(path)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	diags, err := m.Check(context.Background(), path, string(text))
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if len(diags) == 0 {
		fmt.Fprintf(stdout, "%s: no diagnostics\n", path)
		return exitSuccess
	}
	for _, d := range diags {
		fmt.Fprintf(stdout, "%s:%d:%d %s: %s\n", path, d.Range.Start.Line+1, d.Range.Start.Character+1, severityLabel(d.Severity), d.Message)
	}
	return exitSuccess
}

func runLSPGoto(m *lsp.Manager, args []string, stdout, stderr io.Writer) int {
	path, line, col, err := parseFilePos(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	text, err := os.ReadFile(path)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	locs, _, ok, err := m.Navigate(context.Background(), lsp.NavRequest{
		Op:        lsp.NavDefinition,
		Path:      path,
		Line:      line,
		Character: col,
		Text:      string(text),
	})
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if !ok || len(locs) == 0 {
		fmt.Fprintln(stderr, "no definition found")
		return exitSuccess
	}
	for _, loc := range locs {
		fmt.Fprintf(stdout, "%s:%d:%d\n", lsp.URIToPath(loc.URI), loc.Range.Start.Line+1, loc.Range.Start.Character+1)
	}
	return exitSuccess
}

func runLSPReferences(m *lsp.Manager, args []string, stdout, stderr io.Writer) int {
	path, line, col, err := parseFilePos(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	text, err := os.ReadFile(path)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	locs, _, ok, err := m.Navigate(context.Background(), lsp.NavRequest{
		Op:                 lsp.NavReferences,
		Path:               path,
		Line:               line,
		Character:          col,
		Text:               string(text),
		IncludeDeclaration: true,
	})
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if !ok || len(locs) == 0 {
		fmt.Fprintln(stderr, "no references found")
		return exitSuccess
	}
	for _, loc := range locs {
		fmt.Fprintf(stdout, "%s:%d:%d\n", lsp.URIToPath(loc.URI), loc.Range.Start.Line+1, loc.Range.Start.Character+1)
	}
	return exitSuccess
}

func runLSPFind(m *lsp.Manager, args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		return writeExecUsageError(stderr, "usage: green lsp find <symbol>")
	}
	query := strings.Join(args, " ")
	_, symbols, ok, err := m.Navigate(context.Background(), lsp.NavRequest{
		Op:    lsp.NavWorkspaceSymbol,
		Query: query,
	})
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if !ok || len(symbols) == 0 {
		fmt.Fprintln(stderr, "no symbols found")
		return exitSuccess
	}
	for _, s := range symbols {
		fmt.Fprintf(stdout, "%s (%s) — %s:%d:%d\n", s.Name, s.Kind, lsp.URIToPath(s.Location.URI), s.Location.Range.Start.Line+1, s.Location.Range.Start.Character+1)
	}
	return exitSuccess
}

// parseFilePos parses a "file:line:col" positional (line/col 1-based).
func parseFilePos(args []string) (string, int, int, error) {
	if len(args) < 1 {
		return "", 0, 0, fmt.Errorf("usage: green lsp goto <file:line:col>")
	}
	spec := args[0]
	line, col := 1, 1
	if at := strings.LastIndex(spec, ":"); at > 0 {
		rest := spec[at+1:]
		if c := strings.LastIndex(spec[:at], ":"); c > 0 {
			if l, err := strconv.Atoi(spec[c+1 : at]); err == nil {
				line = l
				if cc, err := strconv.Atoi(rest); err == nil {
					col = cc
					spec = spec[:c]
				}
			}
		} else if l, err := strconv.Atoi(rest); err == nil {
			line = l
			spec = spec[:at]
		}
	}
	if !filepath.IsAbs(spec) {
		if wd, err := os.Getwd(); err == nil {
			spec = filepath.Join(wd, spec)
		}
	}
	return spec, line, col, nil
}

func severityLabel(s lsp.DiagnosticSeverity) string {
	switch s {
	case lsp.SeverityError:
		return "error"
	case lsp.SeverityWarning:
		return "warning"
	case lsp.SeverityInformation:
		return "info"
	case lsp.SeverityHint:
		return "hint"
	default:
		return "diagnostic"
	}
}

func writeLSPHelp(out io.Writer) int {
	help := `green lsp — Language Server Protocol from the command line (opencode "LSP for the LLM")

Usage:
  green lsp diagnostics <file>        Show compiler/linter diagnostics for a file
  green lsp goto <file:line:col>      Jump to the definition at a position
  green lsp references <file:line:col> List references to the symbol at a position
  green lsp find <symbol>             Search workspace symbols by name

The workspace root is the nearest git root (or the cwd). Language servers start
lazily per language and degrade gracefully when a server is not installed.
`
	fmt.Fprint(out, help)
	return exitSuccess
}
