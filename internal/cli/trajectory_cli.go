package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/BlusceLabs/green/internal/sessions"
	"github.com/BlusceLabs/green/internal/trajectory"
)

func runTrajectory(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		return writeTrajectoryHelp(stdout)
	}
	if args[0] != "export" {
		return writeExecUsageError(stderr, fmt.Sprintf("unknown trajectory subcommand %q", args[0]))
	}
	var sessionID, outPath string
	compress := false
	asJSONL := false
	maxOut := 0
	for i := 1; i < len(args); i++ {
		switch {
		case args[i] == "--compress":
			compress = true
		case args[i] == "--jsonl":
			asJSONL = true
		case strings.HasPrefix(args[i], "--out="):
			outPath = strings.TrimPrefix(args[i], "--out=")
		case strings.HasPrefix(args[i], "--max-tool-output="):
			fmt.Sscanf(strings.TrimPrefix(args[i], "--max-tool-output="), "%d", &maxOut)
		case strings.HasPrefix(args[i], "-"):
			return writeExecUsageError(stderr, fmt.Sprintf("unknown trajectory flag %q", args[i]))
		default:
			if sessionID == "" {
				sessionID = args[i]
			}
		}
	}
	if sessionID == "" {
		return writeExecUsageError(stderr, "trajectory export requires a session id")
	}
	store := sessions.NewStore(sessions.StoreOptions{})
	traj, err := trajectory.Capture(store, sessionID, trajectory.Options{MaxToolOutput: maxOut})
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if compress {
		traj = traj.Compress(maxOut)
	}
	if outPath == "" {
		// Print summary to stdout when no output file is given.
		fmt.Fprintf(stdout, "session: %s\nsteps: %d\ncompressed: %v\ntokenEstimate: %d\n",
			traj.SessionID, len(traj.Steps), traj.Compressed, traj.TokenEstimate)
		return exitSuccess
	}
	if asJSONL {
		if err := traj.WriteJSONL(outPath); err != nil {
			return writeAppError(stderr, err.Error(), exitCrash)
		}
	} else {
		if err := traj.WriteJSON(outPath); err != nil {
			return writeAppError(stderr, err.Error(), exitCrash)
		}
	}
	fmt.Fprintf(stdout, "Wrote trajectory to %s\n", outPath)
	return exitSuccess
}

func writeTrajectoryHelp(out io.Writer) int {
	help := `green trajectory — capture a session for training & eval (Hermes research-ready)

Usage:
  green trajectory export <session-id> [flags]

Flags:
      --compress            Collapse redundant turns, drop bookkeeping events
      --jsonl               Write one JSON object per step (training pipelines)
      --out=PATH            Write to PATH (JSON unless --jsonl)
      --max-tool-output=N   Truncate tool results to N chars
  -h, --help                Show this help
`
	fmt.Fprint(out, help)
	return exitSuccess
}
