package remote

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// SSHEnvironment runs commands on a remote host over ssh, mirroring Hermes'
// ssh backend. The command is single-quoted and passed as a single remote
// argument so the remote shell expands it; cwd is translated to the configured
// remote workspace root.
type SSHEnvironment struct {
	cfg SSHConfig
}

// SSHConfig is the resolved ssh connection parameters.
type SSHConfig struct {
	Host string
	Args []string
	Root string
}

func openSSH(cfg Config) (*SSHEnvironment, error) {
	if strings.TrimSpace(cfg.SSHHost) == "" {
		return nil, fmt.Errorf("ssh backend selected but remote.backend.sshHost is empty")
	}
	if _, err := exec.LookPath("ssh"); err != nil {
		return nil, fmt.Errorf("ssh backend selected but `ssh` is not on PATH: %w", err)
	}
	return &SSHEnvironment{cfg: SSHConfig{
		Host: cfg.SSHHost,
		Args: cfg.SSHArgs,
		Root: resolveWorkspaceRoot(cfg, ""),
	}}, nil
}

func (s *SSHEnvironment) Name() BackendName { return BackendSSH }

func (s *SSHEnvironment) Run(ctx context.Context, command string, cwd string, env []string) (CommandResult, error) {
	remoteCwd := cwd
	if strings.TrimSpace(remoteCwd) == "" {
		remoteCwd = s.cfg.Root
	}
	// Build a remote command that cd's into the (translated) cwd then runs the
	// user command, all single-quoted for safe transport over ssh.
	remote := fmt.Sprintf("cd %s && %s", quoteArg(remoteCwd), command)
	args := append([]string{}, s.cfg.Args...)
	for _, e := range env {
		// ssh -o SendEnv requires the name only; we pass the full KEY=VALUE via
		// an inline export so the remote shell sees it regardless of server
		// AcceptEnv configuration.
		args = append(args, "-o", "SendEnv="+envName(e))
	}
	args = append(args, s.cfg.Host, remote)
	cmd := exec.CommandContext(ctx, "ssh", args...)
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

func (s *SSHEnvironment) Close() error { return nil }

// envName extracts the KEY from a "KEY=VALUE" environment entry.
func envName(e string) string {
	if i := strings.IndexByte(e, '='); i >= 0 {
		return e[:i]
	}
	return e
}
