package cli

import (
	"strings"
	"testing"

	"github.com/BlusceLabs/green/internal/lsp"
)

func TestParseFilePos(t *testing.T) {
	path, line, col, err := parseFilePos([]string{"/repo/main.go:42:10"})
	if err != nil {
		t.Fatalf("parseFilePos: %v", err)
	}
	if path != "/repo/main.go" || line != 42 || col != 10 {
		t.Fatalf("got %q %d:%d", path, line, col)
	}
	// line-only form
	path, line, col, _ = parseFilePos([]string{"/repo/main.go:5"})
	if path != "/repo/main.go" || line != 5 || col != 1 {
		t.Fatalf("line-only: got %q %d:%d", path, line, col)
	}
}

func TestSeverityLabel(t *testing.T) {
	cases := map[lsp.DiagnosticSeverity]string{
		lsp.SeverityError:       "error",
		lsp.SeverityWarning:     "warning",
		lsp.SeverityInformation: "info",
		lsp.SeverityHint:        "hint",
	}
	for sev, want := range cases {
		if got := severityLabel(sev); got != want {
			t.Fatalf("severityLabel(%d) = %q, want %q", sev, got, want)
		}
	}
}

func TestRunLSPHelp(t *testing.T) {
	var sb strings.Builder
	if code := writeLSPHelp(&sb); code != exitSuccess {
		t.Fatalf("writeLSPHelp code = %d", code)
	}
	if !strings.Contains(sb.String(), "green lsp") {
		t.Fatal("help should mention green lsp")
	}
}
