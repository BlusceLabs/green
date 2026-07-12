// Package remote mirrors Hermes' serverless terminal backends: a single
// BaseEnvironment abstraction over local, docker, ssh, singularity, modal, and
// daytona execution targets. The active backend is selected by the
// TERMINAL_ENV-equivalent config (config.RemoteConfig.Backend) and falls back to
// the local machine when unset or "local".
//
// Each backend implements Environment: Run executes a command string in a working
// directory with an environment, returning captured stdout/stderr and an exit
// code. Backends that are not available on the host (e.g. docker when the daemon
// is absent) report an error from Open rather than silently degrading to local.
package remote

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// BackendName enumerates the supported terminal backends, mirroring Hermes'
// BaseEnvironment subclasses.
type BackendName string

const (
	BackendLocal       BackendName = "local"
	BackendDocker      BackendName = "docker"
	BackendSSH         BackendName = "ssh"
	BackendSingularity BackendName = "singularity"
	BackendModal       BackendName = "modal"
	BackendDaytona     BackendName = "daytona"
)

// AllBackendNames is the ordered list of known backends.
var AllBackendNames = []BackendName{
	BackendLocal,
	BackendDocker,
	BackendSSH,
	BackendSingularity,
	BackendModal,
	BackendDaytona,
}

// IsValidBackend reports whether name is a recognized backend identifier.
func IsValidBackend(name string) bool {
	for _, b := range AllBackendNames {
		if string(b) == name {
			return true
		}
	}
	return false
}

// CommandResult is the captured outcome of a remote command, mirroring the
// local control CommandResult shape so callers can treat local and remote runs
// uniformly.
type CommandResult struct {
	Path     string
	Args     []string
	Stdout   string
	Stderr   string
	ExitCode int
}

// Environment is the Hermes BaseEnvironment equivalent: a single execution
// surface that runs shell command strings in a working directory.
type Environment interface {
	// Name returns the backend identifier.
	Name() BackendName
	// Run executes command in cwd with the supplied environment, returning the
	// captured result. It is the single chokepoint every backend routes through.
	Run(ctx context.Context, command string, cwd string, env []string) (CommandResult, error)
	// Close releases any backend-held resources (sessions, temp dirs).
	Close() error
}

// Config carries the backend selection and per-backend connection details. It is
// the TERMINAL_ENV-equivalent: a single struct that selects and configures the
// terminal environment for a run.
type Config struct {
	// Backend selects the execution target. Empty or "local" uses the host.
	Backend BackendName `json:"backend,omitempty"`
	// DockerImage is the image used by the docker backend (default
	// "ubuntu:latest").
	DockerImage string `json:"dockerImage,omitempty"`
	// DockerArgs are extra arguments appended to `docker run` (e.g. "-p 8080:80").
	DockerArgs []string `json:"dockerArgs,omitempty"`
	// SSHHost is "user@host" or "host" for the ssh backend.
	SSHHost string `json:"sshHost,omitempty"`
	// SSHArgs are extra arguments appended to `ssh` (e.g. "-p 2222").
	SSHArgs []string `json:"sshArgs,omitempty"`
	// SingularityImage is the .sif path or URI for the singularity backend.
	SingularityImage string `json:"singularityImage,omitempty"`
	// SingularityArgs are extra arguments appended to `singularity exec`.
	SingularityArgs []string `json:"singularityArgs,omitempty"`
	// ModalApp is the Modal app/function spec for the modal backend
	// (e.g. "my-app::run").
	ModalApp string `json:"modalApp,omitempty"`
	// ModalArgs are extra arguments appended to the modal CLI invocation.
	ModalArgs []string `json:"modalArgs,omitempty"`
	// DaytonaTarget is the Daytona workspace/instance target for the daytona
	// backend (e.g. "my-workspace").
	DaytonaTarget string `json:"daytonaTarget,omitempty"`
	// DaytonaArgs are extra arguments appended to `daytona ssh`/`run`.
	DaytonaArgs []string `json:"daytonaArgs,omitempty"`
	// WorkspaceRoot is the path on the remote host that corresponds to the local
	// workspace root. Empty means the backend maps cwd 1:1 (docker/singularity
	// mount the workspace; ssh/daytona use the same absolute path when possible).
	WorkspaceRoot string `json:"workspaceRoot,omitempty"`
}

// Open resolves and opens the configured backend. It returns an Environment for
// any non-local backend, or a LocalEnvironment when Backend is empty/"local".
// Open fails fast when a remote backend is selected but its host-side tool (e.g.
// docker, ssh) is unavailable, so a misconfigured run errors loudly instead of
// silently executing on the wrong machine.
func Open(cfg Config) (Environment, error) {
	switch cfg.Backend {
	case "", BackendLocal:
		return &LocalEnvironment{}, nil
	case BackendDocker:
		return openDocker(cfg)
	case BackendSSH:
		return openSSH(cfg)
	case BackendSingularity:
		return openSingularity(cfg)
	case BackendModal:
		return openModal(cfg)
	case BackendDaytona:
		return openDaytona(cfg)
	default:
		return nil, fmt.Errorf("unknown terminal backend %q", cfg.Backend)
	}
}

// LocalEnvironment runs commands on the host machine. It is the default backend
// and the fallback when no TERMINAL_ENV is configured.
type LocalEnvironment struct{}

func (LocalEnvironment) Name() BackendName { return BackendLocal }

func (LocalEnvironment) Run(ctx context.Context, command string, cwd string, env []string) (CommandResult, error) {
	return runLocal(ctx, command, cwd, env)
}

func (LocalEnvironment) Close() error { return nil }

// runLocal executes a shell command string on the host via /bin/sh -c, capturing
// stdout/stderr and the exit code. It is shared by LocalEnvironment and used as
// the inner primitive for backends that shell out to a wrapper (docker/ssh/...).
func runLocal(ctx context.Context, command string, cwd string, env []string) (CommandResult, error) {
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	cmd := buildLocalCommand(ctx, command, cwd, env)
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

// quoteArg single-quotes a string for safe interpolation into a remote shell
// command, escaping embedded single quotes. Mirrors the standard shell-safe
// quoting used by ssh/docker wrappers.
func quoteArg(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
