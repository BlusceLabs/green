package remote

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// DaytonaEnvironment runs commands inside a Daytona workspace, mirroring Hermes'
// daytona backend. It shells out to the `daytona` CLI, which exposes an `ssh`
// subcommand that streams a command into the workspace. When daytonaTarget is
// empty the run fails fast rather than silently executing locally.
type DaytonaEnvironment struct {
	cfg DaytonaConfig
}

// DaytonaConfig is the resolved daytona connection parameters.
type DaytonaConfig struct {
	Target string
	Args   []string
	Root   string
}

func openDaytona(cfg Config) (*DaytonaEnvironment, error) {
	if strings.TrimSpace(cfg.DaytonaTarget) == "" {
		return nil, fmt.Errorf("daytona backend selected but remote.backend.daytonaTarget is empty")
	}
	if _, err := exec.LookPath("daytona"); err != nil {
		return nil, fmt.Errorf("daytona backend selected but `daytona` is not on PATH: %w", err)
	}
	return &DaytonaEnvironment{cfg: DaytonaConfig{
		Target: cfg.DaytonaTarget,
		Args:   cfg.DaytonaArgs,
		Root:   resolveWorkspaceRoot(cfg, ""),
	}}, nil
}

func (d *DaytonaEnvironment) Name() BackendName { return BackendDaytona }

func (d *DaytonaEnvironment) Run(ctx context.Context, command string, cwd string, env []string) (CommandResult, error) {
	remoteCwd := cwd
	if strings.TrimSpace(remoteCwd) == "" {
		remoteCwd = d.cfg.Root
	}
	// `daytona ssh <target> "<command>"` runs the command in the workspace's
	// default working directory; we cd into the translated cwd first.
	remote := fmt.Sprintf("cd %s && %s", quoteArg(remoteCwd), command)
	args := []string{"ssh", d.cfg.Target}
	args = append(args, d.cfg.Args...)
	for _, e := range env {
		// Daytona workspaces inherit the runner's environment; we export inline
		// so the command sees the variable regardless of workspace config.
		remote = e + " " + remote
	}
	args = append(args, remote)
	cmd := exec.CommandContext(ctx, "daytona", args...)
	out, err := cmd.CombinedOutput()
	res := CommandResult{
		Path:     cmd.Path,
		Args:     cmd.Args,
		Stdout:   string(out),
		ExitCode: exitCodeOf(cmd, err),
	}
	if err != nil && res.ExitCode == 0 {
		res.ExitCode = -1
	}
	return res, nil
}

func (d *DaytonaEnvironment) Close() error { return nil }
