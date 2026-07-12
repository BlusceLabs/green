package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/BlusceLabs/green/internal/redaction"
	"github.com/BlusceLabs/green/internal/search"
	"github.com/BlusceLabs/green/internal/sessions"
)

// runRecall searches past local sessions for a query and synthesizes a
// cross-session answer from the top hits (Hermes's "search your own past
// conversations" feature). With --llm the synthesis is upgraded by a model;
// without it the top hits are summarized deterministically.
func runRecall(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	query := strings.TrimSpace(strings.Join(args, " "))
	limit := 10
	asJSON := false
	useLLM := false
	rest := args
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "--json":
			asJSON = true
		case a == "--llm":
			useLLM = true
		case strings.HasPrefix(a, "--limit="):
			fmt.Sscanf(strings.TrimPrefix(a, "--limit="), "%d", &limit)
		case a == "-h" || a == "--help" || a == "help":
			return writeRecallHelp(stdout)
		case !strings.HasPrefix(a, "-"):
			// First non-flag is the query; gather the rest as the query too.
			query = strings.TrimSpace(strings.Join(rest[i:], " "))
			i = len(rest)
		}
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return writeExecUsageError(stderr, "recall requires a query")
	}

	store := sessions.NewStore(sessions.StoreOptions{})
	result, err := search.Sessions(query, search.Options{
		Store:   store,
		Limit:   limit,
		Reindex: true,
	})
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if asJSON {
		out, _ := json.MarshalIndent(result, "", "  ")
		fmt.Fprintln(stdout, string(out))
		return exitSuccess
	}
	if len(result.Hits) == 0 {
		fmt.Fprintf(stdout, "No past sessions matched %q.\n", query)
		return exitSuccess
	}
	fmt.Fprintf(stdout, "Found %d past-session hits for %q:\n\n", result.TotalHits, query)
	excerpts := make([]string, 0, len(result.Hits))
	for i, hit := range result.Hits {
		ctx := strings.Join(strings.Fields(redaction.RedactString(hit.Context, redaction.Options{})), " ")
		excerpts = append(excerpts, ctx)
		fmt.Fprintf(stdout, "%d. [%s] %s\n   %s\n\n", i+1, hit.Session.SessionID, hit.Session.Title, ctx)
	}
	if useLLM {
		provider, perr := resolveActiveProvider(deps)
		if perr != nil {
			fmt.Fprintf(stderr, "recall: %s\n", perr.Error())
		} else {
			answer, serr := synthesizeRecall(provider, query, excerpts)
			if serr != nil {
				fmt.Fprintf(stderr, "recall: llm synthesis failed: %s\n", serr.Error())
			} else if answer != "" {
				fmt.Fprintf(stdout, "Synthesis:\n%s\n", answer)
				return exitSuccess
			}
		}
	}
	fmt.Fprintln(stdout, "Synthesis: the above are the closest past moments. Reuse the relevant approach rather than starting cold.")
	return exitSuccess
}

func writeRecallHelp(out io.Writer) int {
	help := `green recall — search your own past sessions and synthesize (Hermes recall)

Usage:
  green recall "what did we decide about X" [flags]

Flags:
      --limit=N   Max hits to consider (default 10)
      --llm       Synthesize the answer with the active model (requires a provider)
      --json      Emit the raw search result as JSON
  -h, --help      Show this help
`
	fmt.Fprint(out, help)
	return exitSuccess
}
