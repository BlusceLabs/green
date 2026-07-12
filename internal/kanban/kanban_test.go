package kanban

import "testing"

func TestBoardCRUD(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	b, err := s.CreateBoard("work")
	if err != nil {
		t.Fatalf("CreateBoard: %v", err)
	}
	c, err := s.AddCard(b.ID, "first", "do the first thing", PriorityMed, nil, nil)
	if err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	// The store re-loads from disk on every call, so inspect via GetBoard.
	got, err := s.GetBoard(b.ID)
	if err != nil {
		t.Fatalf("GetBoard: %v", err)
	}
	if len(got.Cards) != 1 {
		t.Fatalf("expected 1 card, got %d", len(got.Cards))
	}
	if _, err := s.SetStatus(c.ID, StatusDoing); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if _, err := s.AddComment(c.ID, "agent", "in progress"); err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	// Reload and confirm persistence.
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("Open reload: %v", err)
	}
	b2, err := s2.GetBoard(b.ID)
	if err != nil {
		t.Fatalf("GetBoard reload: %v", err)
	}
	if len(b2.Cards) != 1 {
		t.Fatal("reload lost cards")
	}
	if b2.Cards[0].Status != StatusDoing {
		t.Fatalf("reload lost status: %q", b2.Cards[0].Status)
	}
	if len(b2.Cards[0].Comments) != 1 || b2.Cards[0].Comments[0].Body != "in progress" {
		t.Fatal("reload lost comment")
	}
}

func TestDepsStored(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	b, _ := s.CreateBoard("d")
	dep, _ := s.AddCard(b.ID, "dep", "blocker", PriorityLow, nil, nil)
	task, _ := s.AddCard(b.ID, "task", "needs dep", PriorityLow, nil, []string{dep.ID})
	if len(task.Dependencies) != 1 || task.Dependencies[0] != dep.ID {
		t.Fatalf("dependency not stored: %v", task.Dependencies)
	}
	// Re-fetch the board: the store re-loads from disk on every call.
	got, err := s.GetBoard(b.ID)
	if err != nil {
		t.Fatalf("GetBoard: %v", err)
	}
	// An unfinished dependency means the task is not yet runnable.
	runnable := func() bool {
		for _, c := range got.Cards {
			if c.ID == task.ID {
				if c.Status == StatusDone || c.Status == StatusBlocked {
					return false
				}
				for _, depID := range c.Dependencies {
					for _, d := range got.Cards {
						if d.ID == depID && d.Status != StatusDone {
							return false
						}
					}
				}
			}
		}
		return true
	}
	if runnable() {
		t.Fatal("task should be blocked while dependency is unfinished")
	}
	_, _ = s.SetStatus(dep.ID, StatusDone)
	got, _ = s.GetBoard(b.ID)
	if !runnable() {
		t.Fatal("task should be runnable after dependency is done")
	}
}
