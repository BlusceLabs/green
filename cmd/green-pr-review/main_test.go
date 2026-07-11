package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunBuildsMarkdownFromEnvironment(t *testing.T) {
	env := []string{
		"green_PR_NUMBER=50",
		"green_REVIEW_HEAD_SHA=abcdef1234567890",
		"green_REVIEW_DIFF_CHECK=success",
		"green_REVIEW_TEST=success",
		"green_REVIEW_BUILD=success",
		"green_REVIEW_SMOKE=success",
		"green_CHANGED_FILES=b.go\na.go",
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run(nil, env, &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d: %s", exitCode, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"<!-- green-auto-review -->",
		"Verdict: **No blockers found**",
		"Head: `abcdef123456`",
		"Changed files (2): `a.go`, `b.go`",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %q:\n%s", want, output)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRunHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{"--help"}, nil, &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d: %s", exitCode, stderr.String())
	}
	for _, want := range []string{"green-pr-review", "green_REVIEW_DIFF_CHECK", "green_CHANGED_FILES"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected help to contain %q:\n%s", want, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRunRejectsUnknownArgs(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{"--json"}, nil, &stdout, &stderr)

	if exitCode == 0 {
		t.Fatalf("expected non-green exit code")
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), `unknown flag "--json"`) {
		t.Fatalf("expected unknown flag error, got %q", stderr.String())
	}
}
