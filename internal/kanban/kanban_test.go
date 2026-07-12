package kanban

import "testing"

func TestBoardCRUD(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	b := s.CreateBoard("work", "My work board")
	if len(s.Boards) != 1 {
		t.Fatalf("expected 1 board, got %d", len(s.Boards))
	}
	c := b.AddCard("first", "do the first thing", PriorityMedium, "100")
	if len(b.Cards) != 1 {
		t.Fatalf("expected 1 card, got %d", len(b.Cards))
	}
	c.SetStatus(StatusDoing)
	if b.Cards[0].Status != StatusDoing {
		t.Fatalf("status = %q want %q", b.Cards[0].Status, StatusDoing)
	}
	c.Comment("in progress")
	if len(b.Cards[0].Comments) != 1 {
		t.Fatal("comment not recorded")
	}
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Reload and confirm persistence.
	s2, err := Load(dir)
	if err != nil {
		t.Fatalf("Load reload: %v", err)
	}
	if len(s2.Boards) != 1 || len(s2.Boards[0].Cards) != 1 {
		t.Fatal("reload lost data")
	}
	if s2.Boards[0].Cards[0].Status != StatusDoing {
		t.Fatal("reload lost status")
	}
}

func TestDepsBlockCompletion(t *testing.T) {
	dir := t.TempDir()
	s, _ := Load(dir)
	b := s.CreateBoard("d", "deps")
	dep := b.AddCard("dep", "blocker", PriorityLow, "1")
	task := b.AddCard("task", "needs dep", PriorityLow, "2")
	task.Deps = []string{dep.ID}
	if next := b.NextRunnable(); next == nil || next.ID != dep.ID {
		t.Fatalf("NextRunnable should be the dependency first, got %v", next)
	}
	dep.SetStatus(StatusDone)
	if next := b.NextRunnable(); next == nil || next.ID != task.ID {
		t.Fatalf("after dep done, task should be runnable, got %v", next)
	}
}
