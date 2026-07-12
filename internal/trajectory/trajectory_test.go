package trajectory

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/BlusceLabs/green/internal/sessions"
)

func captureSession(t *testing.T) *sessions.Store {
	t.Helper()
	dir := t.TempDir()
	store := sessions.NewStore(sessions.StoreOptions{RootDir: dir, Now: func() time.Time {
		return time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC)
	}})
	sid, err := store.Create(sessions.CreateInput{SessionID: "testsess", Cwd: "test-cwd", ModelID: "gpt", Provider: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	sessionID := sid.SessionID
	write := func(typ sessions.EventType, payload string) {
		input := sessions.AppendEventInput{Type: typ, Payload: json.RawMessage(payload)}
		if _, err := store.AppendEvent(sessionID, input); err != nil {
			t.Fatal(err)
		}
	}
	write(sessions.EventMessage, `{"role":"user","content":"set up ci for the project"}`)
	write(sessions.EventToolCall, `{"name":"exec_command","arguments":"write github action"}`)
	write(sessions.EventToolResult, `{"name":"exec_command","content":"`+longString(5000)+`"}`)
	write(sessions.EventMessage, `{"role":"assistant","content":"done"}`)
	write(sessions.EventSessionCheckpoint, `{}`)
	return store
}

func longString(n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = 'a'
	}
	return string(out)
}

func TestCaptureAndCompress(t *testing.T) {
	store := captureSession(t)
	traj, err := Capture(store, "testsess", Options{MaxToolOutput: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(traj.Steps) != 5 {
		t.Fatalf("expected 5 steps, got %d", len(traj.Steps))
	}
	if traj.TokenEstimate <= 0 {
		t.Fatalf("expected positive token estimate, got %d", traj.TokenEstimate)
	}

	// Compression should drop the checkpoint meta event.
	compressed := traj.Compress(100)
	if !compressed.Compressed {
		t.Fatalf("expected compressed flag")
	}
	if len(compressed.Steps) != 4 {
		t.Fatalf("expected 4 steps after compress, got %d", len(compressed.Steps))
	}
	// Tool result should be truncated to ~100 chars + marker.
	var toolStep *Step
	for i := range compressed.Steps {
		if compressed.Steps[i].Role == "tool" {
			toolStep = &compressed.Steps[i]
		}
	}
	if toolStep == nil {
		t.Fatalf("tool step missing after compress")
	}
	if len(toolStep.Content) > 120 || !toolStep.Truncated {
		t.Fatalf("tool output not truncated: len=%d truncated=%v", len(toolStep.Content), toolStep.Truncated)
	}
}

func TestCaptureMissingSession(t *testing.T) {
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	if _, err := Capture(store, "nope", Options{}); err == nil {
		t.Fatalf("expected error for missing session")
	}
}

func TestWriteJSONAndJSONL(t *testing.T) {
	store := captureSession(t)
	traj, _ := Capture(store, "testsess", Options{MaxToolOutput: 0})
	dir := t.TempDir()
	if err := traj.WriteJSON(dir + "/t.json"); err != nil {
		t.Fatal(err)
	}
	if err := traj.WriteJSONL(dir + "/t.jsonl"); err != nil {
		t.Fatal(err)
	}
}
