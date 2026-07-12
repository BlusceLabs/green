package gateway

import "testing"

func TestAccessRequestApproveRoles(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadTelegramAccess(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// First user to be approved becomes admin.
	code, approved := s.RequestAccess("alice", "100")
	if approved || code == "" {
		t.Fatalf("first request should be pending with a code (approved=%v code=%q)", approved, code)
	}
	if !s.IsPending("100") {
		t.Fatal("user should be pending")
	}
	if err := s.Approve(code); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if !s.IsApproved("100") {
		t.Fatal("user should be approved after approve")
	}
	if s.Role("100") != RoleAdmin {
		t.Fatalf("first approved user should be admin, got %q", s.Role("100"))
	}

	// Second user is a member.
	code2, _ := s.RequestAccess("bob", "200")
	if err := s.Approve(code2); err != nil {
		t.Fatalf("Approve bob: %v", err)
	}
	if s.Role("200") != RoleMember {
		t.Fatalf("second user should be member, got %q", s.Role("200"))
	}

	// Promote/demote.
	if err := s.Promote("200"); err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if s.Role("200") != RoleAdmin {
		t.Fatal("promote should make bob admin")
	}
	if err := s.Demote("200"); err != nil {
		t.Fatalf("Demote: %v", err)
	}
	if s.Role("200") != RoleMember {
		t.Fatal("demote should make bob member")
	}

	// Remove.
	if err := s.Remove("200"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if s.IsApproved("200") {
		t.Fatal("removed user should not be approved")
	}

	// Reject a pending request.
	s.RequestAccess("carol", "300")
	if err := s.Reject("300"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if s.IsPending("300") {
		t.Fatal("rejected request should not be pending")
	}

	// Reset clears everything.
	if err := s.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	users, pending := s.List()
	if len(users) != 0 || len(pending) != 0 {
		t.Fatalf("reset should clear all (users=%d pending=%d)", len(users), len(pending))
	}
}

func TestAccessApproveByUserID(t *testing.T) {
	dir := t.TempDir()
	s, _ := LoadTelegramAccess(dir)
	s.RequestAccess("dave", "400")
	if err := s.Approve("400"); err != nil {
		t.Fatalf("Approve by user id: %v", err)
	}
	if !s.IsApproved("400") {
		t.Fatal("approve by user id should work")
	}
}
