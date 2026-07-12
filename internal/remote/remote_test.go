package remote

import (
	"context"
	"strings"
	"testing"
)

func TestOpenLocalIsDefault(t *testing.T) {
	for _, backend := range []BackendName{"", BackendLocal} {
		env, err := Open(Config{Backend: backend})
		if err != nil {
			t.Fatalf("Open(%q) returned error: %v", backend, err)
		}
		if env.Name() != BackendLocal {
			t.Fatalf("Open(%q).Name() = %q, want local", backend, env.Name())
		}
	}
}

func TestLocalEnvironmentRunsCommand(t *testing.T) {
	env, err := Open(Config{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := env.Run(context.Background(), "echo hello && exit 0", t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", res.ExitCode)
	}
	if !strings.Contains(res.Stdout, "hello") {
		t.Fatalf("stdout = %q, want it to contain hello", res.Stdout)
	}
}

func TestLocalEnvironmentCapturesExitCode(t *testing.T) {
	env, err := Open(Config{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := env.Run(context.Background(), "exit 7", t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.ExitCode != 7 {
		t.Fatalf("exit code = %d, want 7", res.ExitCode)
	}
}

func TestOpenUnknownBackendErrors(t *testing.T) {
	if _, err := Open(Config{Backend: "nonsense"}); err == nil {
		t.Fatal("Open(nonsense) expected error, got nil")
	}
}

func TestIsValidBackend(t *testing.T) {
	for _, b := range AllBackendNames {
		if !IsValidBackend(string(b)) {
			t.Fatalf("IsValidBackend(%q) = false, want true", b)
		}
	}
	if IsValidBackend("bogus") {
		t.Fatal("IsValidBackend(bogus) = true, want false")
	}
}

func TestQuoteArgEscapesSingleQuotes(t *testing.T) {
	got := quoteArg("it's a test")
	want := `'it'\''s a test'`
	if got != want {
		t.Fatalf("quoteArg = %q, want %q", got, want)
	}
}

func TestRemoteBackendsFailWithoutTool(t *testing.T) {
	cases := []Config{
		{Backend: BackendDocker},
		{Backend: BackendSSH},
		{Backend: BackendSingularity},
		{Backend: BackendModal},
		{Backend: BackendDaytona},
	}
	for _, cfg := range cases {
		if _, err := Open(cfg); err == nil {
			t.Fatalf("Open(%+v) expected error (missing tool/host), got nil", cfg)
		}
	}
}
