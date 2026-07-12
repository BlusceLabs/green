package contextfiles

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAndRender(t *testing.T) {
	root := t.TempDir()
	userDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ProjectFileName), []byte("# Project notes\nUse Go 1.25.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := Load(root, userDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Scope != ScopeProject {
		t.Fatalf("unexpected files: %+v", files)
	}
	block := RenderBlock(files)
	if block == "" || !contains(block, "Go 1.25") {
		t.Fatalf("render missing content: %q", block)
	}
}

func TestLoadMissingSkipped(t *testing.T) {
	root := t.TempDir()
	userDir := t.TempDir()
	files, err := Load(root, userDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("expected no files, got %+v", files)
	}
}

func TestAppendCreatesFile(t *testing.T) {
	root := t.TempDir()
	userDir := t.TempDir()
	path, err := Append(ScopeProject, root, userDir, "We deploy on Fridays.")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(data), "- We deploy on Fridays.") {
		t.Fatalf("append content wrong: %q", string(data))
	}
	// Append again; should not duplicate the heading, just add a bullet.
	if _, err := Append(ScopeProject, root, userDir, "CI runs on main only."); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if countOccurrences(string(data), "- ") != 2 {
		t.Fatalf("expected 2 bullets, got %q", string(data))
	}
}

func TestListScopes(t *testing.T) {
	root := t.TempDir()
	userDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, string(ProjectFileName)), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ListScopes(root, userDir); len(got) != 1 || got[0] != ScopeProject {
		t.Fatalf("ListScopes wrong: %+v", got)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func countOccurrences(haystack, needle string) int {
	count := 0
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			count++
			i += len(needle) - 1
		}
	}
	return count
}
