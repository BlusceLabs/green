package remote

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// buildLocalCommand constructs an *exec.Cmd that runs command via the host
// shell in cwd with the supplied environment. When env is non-empty it replaces
// (not appends to) the process environment so callers control the full set.
func buildLocalCommand(ctx context.Context, command string, cwd string, env []string) *exec.Cmd {
	shell := "/bin/sh"
	shellArgs := []string{"-c", command}
	if runtime.GOOS == "windows" {
		shell = "cmd.exe"
		shellArgs = []string{"/c", command}
	}
	cmd := exec.CommandContext(ctx, shell, shellArgs...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	if len(env) > 0 {
		cmd.Env = append([]string{}, env...)
	}
	return cmd
}

// exitCodeOf extracts the exit code from a finished command, handling the
// nil-error (success) and *exec.ExitError (non-zero) cases.
func exitCodeOf(cmd *exec.Cmd, err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errAs(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// errAs is a tiny wrapper so the helper above reads cleanly; it forwards to
// errors.As.
func errAs(err error, target **exec.ExitError) bool {
	if e, ok := err.(*exec.ExitError); ok {
		*target = e
		return true
	}
	return false
}

// resolveWorkspaceRoot returns the configured remote workspace root, falling back
// to the local cwd when unset. Backends use it to translate a local path into
// the corresponding remote path.
func resolveWorkspaceRoot(cfg Config, localCwd string) string {
	if strings.TrimSpace(cfg.WorkspaceRoot) != "" {
		return cfg.WorkspaceRoot
	}
	if localCwd != "" {
		return localCwd
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}
