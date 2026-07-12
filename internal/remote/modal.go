package remote

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// ModalEnvironment runs commands on Modal's serverless infrastructure, mirroring
// Hermes' modal backend. It shells out to the `modal` CLI, invoking the
// configured app/function with the command as an argument. When modalApp is
// empty the run fails fast rather than silently executing locally.
type ModalEnvironment struct {
	cfg ModalConfig
}

// ModalConfig is the resolved modal connection parameters.
type ModalConfig struct {
	App  string
	Args []string
	Root string
}

func openModal(cfg Config) (*ModalEnvironment, error) {
	if strings.TrimSpace(cfg.ModalApp) == "" {
		return nil, fmt.Errorf("modal backend selected but remote.backend.modalApp is empty")
	}
	if _, err := exec.LookPath("modal"); err != nil {
		return nil, fmt.Errorf("modal backend selected but `modal` is not on PATH: %w", err)
	}
	return &ModalEnvironment{cfg: ModalConfig{
		App:  cfg.ModalApp,
		Args: cfg.ModalArgs,
		Root: resolveWorkspaceRoot(cfg, ""),
	}}, nil
}

func (m *ModalEnvironment) Name() BackendName { return BackendModal }

func (m *ModalEnvironment) Run(ctx context.Context, command string, cwd string, env []string) (CommandResult, error) {
	remoteCwd := cwd
	if strings.TrimSpace(remoteCwd) == "" {
		remoteCwd = m.cfg.Root
	}
	args := []string{"run", m.cfg.App}
	args = append(args, m.cfg.Args...)
	for _, e := range env {
		args = append(args, "--env", e)
	}
	// Pass the command and cwd as trailing args to the modal function, which is
	// expected to exec them in the function's working directory.
	args = append(args, "--", command, remoteCwd)
	cmd := exec.CommandContext(ctx, "modal", args...)
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

func (m *ModalEnvironment) Close() error { return nil }
