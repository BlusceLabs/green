package remote

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// SingularityEnvironment runs commands inside a Singularity/Apptainer
// container, mirroring Hermes' singularity backend. The workspace root is
// bind-mounted into the container at the same absolute path.
type SingularityEnvironment struct {
	cfg   Config
	image string
}

func openSingularity(cfg Config) (*SingularityEnvironment, error) {
	if strings.TrimSpace(cfg.SingularityImage) == "" {
		return nil, fmt.Errorf("singularity backend selected but remote.backend.singularityImage is empty")
	}
	if _, err := exec.LookPath("singularity"); err != nil {
		// Apptainer ships a `singularity` compatibility binary on most installs;
		// fall back to it when present.
		if _, err2 := exec.LookPath("apptainer"); err2 != nil {
			return nil, fmt.Errorf("singularity backend selected but `singularity`/`apptainer` is not on PATH: %w", err)
		}
	}
	return &SingularityEnvironment{cfg: cfg, image: cfg.SingularityImage}, nil
}

func (s *SingularityEnvironment) Name() BackendName { return BackendSingularity }

func (s *SingularityEnvironment) Run(ctx context.Context, command string, cwd string, env []string) (CommandResult, error) {
	root := resolveWorkspaceRoot(s.cfg, cwd)
	binary := "singularity"
	if _, err := exec.LookPath("singularity"); err != nil {
		binary = "apptainer"
	}
	args := []string{"exec", "--bind", root + ":" + root, "--pwd", root}
	args = append(args, s.cfg.SingularityArgs...)
	for _, e := range env {
		args = append(args, "--env", e)
	}
	args = append(args, s.image, "sh", "-c", command)
	cmd := exec.CommandContext(ctx, binary, args...)
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

func (s *SingularityEnvironment) Close() error { return nil }
