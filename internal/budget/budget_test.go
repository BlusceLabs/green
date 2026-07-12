package budget

import (
	"path/filepath"
	"testing"

	"github.com/BlusceLabs/green/internal/greenruntime"
)

func TestRecordAndStatus(t *testing.T) {
	dir := t.TempDir()
	tr, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if tr.Exceeded() {
		t.Fatal("fresh tracker should not be exceeded (limit 0 = unlimited)")
	}
	tr.Record(greenruntime.Usage{InputTokens: 10, OutputTokens: 5})
	tr.Record(greenruntime.Usage{InputTokens: 40, OutputTokens: 45})
	if got := tr.Status().Used; got != 100 {
		t.Fatalf("used = %d, want 100", got)
	}
	// Persistence: reload and confirm the usage survived.
	tr2, err := New(dir)
	if err != nil {
		t.Fatalf("New reload: %v", err)
	}
	if got := tr2.Status().Used; got != 100 {
		t.Fatalf("reloaded used = %d, want 100", got)
	}
	_ = filepath.Join(dir, FileName)
}

func TestLimitEnforcement(t *testing.T) {
	dir := t.TempDir()
	tr, _ := New(dir)
	if err := tr.SetLimit(50); err != nil {
		t.Fatalf("SetLimit: %v", err)
	}
	if tr.Exceeded() {
		t.Fatal("should not exceed before recording")
	}
	tr.Record(greenruntime.Usage{InputTokens: 50, OutputTokens: 10})
	if !tr.Exceeded() {
		t.Fatal("expected exceeded after crossing limit")
	}
	// Override lets the run continue.
	if err := tr.SetOverride(true); err != nil {
		t.Fatalf("SetOverride: %v", err)
	}
	if tr.Exceeded() {
		t.Fatal("override should lift the exceeded state")
	}
	if err := tr.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if tr.Status().Used != 0 || tr.Exceeded() {
		t.Fatal("reset should zero usage and clear override")
	}
}

func TestLimitPersists(t *testing.T) {
	dir := t.TempDir()
	tr, _ := New(dir)
	_ = tr.SetLimit(123)
	tr2, _ := New(dir)
	if tr2.Limit() != 123 {
		t.Fatalf("limit not persisted: got %d want 123", tr2.Limit())
	}
}
