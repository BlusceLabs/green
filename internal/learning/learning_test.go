package learning

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s := NewStore(dir)
	s.Now = func() time.Time { return time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC) }
	if err := s.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	return s
}

func TestAppendAndListMemory(t *testing.T) {
	s := newTestStore(t)
	e, err := s.AppendMemory("", "I prefer Go over Python for CLIs", CategoryPreference, "sess1")
	if err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}
	if e.ID == "" || e.Confidence != 1.0 {
		t.Fatalf("unexpected entry: %+v", e)
	}
	// Update existing.
	if _, err := s.AppendMemory(e.ID, "I prefer Go for CLIs", "", "sess1"); err != nil {
		t.Fatalf("update: %v", err)
	}
	memories, err := s.ListMemories("")
	if err != nil {
		t.Fatalf("ListMemories: %v", err)
	}
	if len(memories) != 1 {
		t.Fatalf("want 1 memory, got %d", len(memories))
	}
	if memories[0].Content != "I prefer Go for CLIs" {
		t.Fatalf("content not updated: %q", memories[0].Content)
	}
}

func TestListMemoryFiltersByCategory(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.AppendMemory("", "fact a", CategoryFact, "s"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMemory("", "pref b", CategoryPreference, "s"); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListMemories(CategoryPreference)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Category != CategoryPreference {
		t.Fatalf("category filter wrong: %+v", got)
	}
}

func TestUnknownCategoryNormalized(t *testing.T) {
	s := newTestStore(t)
	e, err := s.AppendMemory("", "x", MemoryCategory("nonsense"), "s")
	if err != nil {
		t.Fatal(err)
	}
	if e.Category != CategoryFact {
		t.Fatalf("want fact fallback, got %q", e.Category)
	}
}

func TestRemoveMemory(t *testing.T) {
	s := newTestStore(t)
	e, _ := s.AppendMemory("", "x", CategoryFact, "s")
	ok, err := s.RemoveMemory(e.ID)
	if err != nil || !ok {
		t.Fatalf("RemoveMemory: ok=%v err=%v", ok, err)
	}
	memories, _ := s.ListMemories("")
	if len(memories) != 0 {
		t.Fatalf("want 0 memories, got %d", len(memories))
	}
}

func TestProfileMergeDedup(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.UpdateProfile([]string{"uses go", "uses go"}, nil, []string{"Go", "go"}); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetProfile()
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Facts) != 1 || p.Facts[0] != "uses go" {
		t.Fatalf("facts dedup wrong: %+v", p.Facts)
	}
	if len(p.Topics) != 1 || p.Topics[0] != "Go" {
		t.Fatalf("topics dedup wrong: %+v", p.Topics)
	}
}

func TestNudgeLifecycle(t *testing.T) {
	s := newTestStore(t)
	NudgeInterval = time.Hour
	defer func() { NudgeInterval = defaultNudgeInterval }()
	status, err := s.Nudge()
	if err != nil {
		t.Fatal(err)
	}
	if !status.Due {
		t.Fatalf("first nudge should be due with zero last-nudge")
	}
	if err := s.MarkNudgePending(); err != nil {
		t.Fatal(err)
	}
	status, _ = s.Nudge()
	if !status.Pending {
		t.Fatalf("nudge should be pending")
	}
	if err := s.AcknowledgeNudge(); err != nil {
		t.Fatal(err)
	}
	status, _ = s.Nudge()
	if status.Pending {
		t.Fatalf("nudge should be cleared")
	}
	if !status.LastNudge.Equal(s.Now()) {
		t.Fatalf("last nudge not refreshed")
	}
}

func TestReflectCuratesMemoryAndProfile(t *testing.T) {
	s := newTestStore(t)
	turns := []Turn{
		{Role: "user", Content: "We use Postgres for the main DB. I prefer small PRs."},
		{Role: "assistant", Content: "ok"},
		{Role: "tool", Content: "ran migration"},
		{Role: "tool", Content: "ran tests"},
	}
	rep, err := s.Reflect(Transcript{SessionID: "sess-1", Turns: turns})
	if err != nil {
		t.Fatalf("Reflect: %v", err)
	}
	if rep.MemoriesAdded == 0 {
		t.Fatalf("expected memories extracted, got %d", rep.MemoriesAdded)
	}
	p, _ := s.GetProfile()
	if len(p.Topics) == 0 {
		t.Fatalf("expected topics extracted, got %+v", p.Topics)
	}
}

func TestReflectCreatesSkillForComplexTask(t *testing.T) {
	s := newTestStore(t)
	turns := []Turn{
		{Role: "user", Content: "Set up a ci pipeline for the green project"},
		{Role: "tool", Content: "wrote github action yaml"},
		{Role: "tool", Content: "committed file"},
		{Role: "tool", Content: "opened pull request"},
		{Role: "tool", Content: "ran lint"},
	}
	rep, err := s.Reflect(Transcript{SessionID: "sess-2", Turns: turns})
	if err != nil {
		t.Fatalf("Reflect: %v", err)
	}
	if !rep.SkillCreated {
		t.Fatalf("expected a skill to be created for complex task")
	}
	// The skill file should exist on disk.
	matches, _ := filepath.Glob(filepath.Join(s.SkillsDir, "*", "SKILL.md"))
	if len(matches) == 0 {
		t.Fatalf("expected a SKILL.md under %s", s.SkillsDir)
	}
}

func TestReflectMarksNudgeWhenNoKnowledge(t *testing.T) {
	s := newTestStore(t)
	turns := []Turn{
		{Role: "user", Content: "just chatting about the weather today"},
		{Role: "tool", Content: "a"},
		{Role: "tool", Content: "b"},
		{Role: "tool", Content: "c"},
		{Role: "tool", Content: "d"},
	}
	rep, err := s.Reflect(Transcript{SessionID: "sess-3", Turns: turns})
	if err != nil {
		t.Fatalf("Reflect: %v", err)
	}
	if !rep.NudgeMarked {
		t.Fatalf("expected nudge to be marked when no durable knowledge was captured")
	}
}

func TestRenderBlocks(t *testing.T) {
	s := newTestStore(t)
	s.AppendMemory("", "I prefer tabs", CategoryPreference, "s")
	s.UpdateProfile([]string{"works at Acme"}, nil, []string{"Go"})
	mem, err := s.RenderMemoryBlock()
	if err != nil || mem == "" {
		t.Fatalf("memory block empty: %v", err)
	}
	prof, err := s.RenderProfileBlock()
	if err != nil || prof == "" {
		t.Fatalf("profile block empty: %v", err)
	}
	if !contains(mem, "prefer tabs") || !contains(prof, "Acme") {
		t.Fatalf("blocks missing content:\nMEM:%s\nPROF:%s", mem, prof)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
