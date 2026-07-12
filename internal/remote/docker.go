package remote

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// DockerEnvironment runs commands inside a throwaway container, mirroring Hermes'
// docker backend. The workspace root is bind-mounted into the container at the
// same absolute path so cwd translation is a no-op.
type DockerEnvironment struct {
	cfg  Config
	image string
}

func openDocker(cfg Config) (*DockerEnvironment, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, fmt.Errorf("docker backend selected but `docker` is not on PATH: %w", err)
	}
	image := strings.TrimSpace(cfg.DockerImage)
	if image == "" {
		image = "ubuntu:latest"
	}
	return &DockerEnvironment{cfg: cfg, image: image}, nil
}

func (d *DockerEnvironment) Name() BackendName { return BackendDocker }

func (d *DockerEnvironment) Run(ctx context.Context, command string, cwd string, env []string) (CommandResult, error) {
	root := resolveWorkspaceRoot(d.cfg, cwd)
	args := []string{"run", "--rm", "-w", root, "-v", root + ":" + root}
	args = append(args, d.cfg.DockerArgs...)
	for _, e := range env {
		args = append(args, "-e", e)
	}
	args = append(args, d.image, "sh", "-c", command)
	cmd := exec.CommandContext(ctx, "docker", args...)
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

func (d *DockerEnvironment) Close() error { return nil }

// dockerAvailable reports whether a docker daemon is reachable, used by the
// backend auto-selection diagnostics.
func dockerAvailable() bool {
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	cmd := exec.Command("docker", "info")
	cmd.Stderr = nil
	return cmd.Run() == nil
}
