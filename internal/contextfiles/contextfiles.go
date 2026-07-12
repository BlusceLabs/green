// Package contextfiles implements Hermes-style "context files": durable,
// project-scoped knowledge that shapes every conversation (separate from the
// executable AGENTS.md instructions). A context file is a plain markdown file
// the agent and the user append to over time — project conventions that aren't
// strict rules, running notes, decisions, and gotchas that should resurface in
// future sessions.
//
// Two scopes are supported:
//   - project: <projectRoot>/CONTEXT.md  (committed, shared with the team)
//   - user:    <configDir>/green/context.md (follows you across projects)
//
// Both are merged into the system prompt so the agent remembers project context
// without re-discovering it each session.
package contextfiles

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Scope names the origin of a context file.
type Scope string

const (
	ScopeProject Scope = "project"
	ScopeUser    Scope = "user"
)

// ProjectFileName and UserFileName are the on-disk file names.
const (
	ProjectFileName = "CONTEXT.md"
	UserFileName    = "context.md"
)

// File is a resolved context file and its parsed entries.
type File struct {
	Scope   Scope  `json:"scope"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

// ResolvePaths returns the context file paths for each scope. projectRoot may be
// empty (no project file); userConfigDir is the directory returned by
// config.UserConfigDir() (or any dir provider). The project path is returned
// even when the file does not exist yet so callers can create it.
func ResolvePaths(projectRoot string, userConfigDir string) map[Scope]string {
	paths := map[Scope]string{}
	if strings.TrimSpace(projectRoot) != "" {
		paths[ScopeProject] = filepath.Join(projectRoot, ProjectFileName)
	}
	if strings.TrimSpace(userConfigDir) != "" {
		paths[ScopeUser] = filepath.Join(userConfigDir, "green", UserFileName)
	}
	return paths
}

// Load reads every context file that exists, returning one entry per scope in a
// stable (project, then user) order. A missing file is simply skipped.
func Load(projectRoot string, userConfigDir string) ([]File, error) {
	paths := ResolvePaths(projectRoot, userConfigDir)
	out := make([]File, 0, 2)
	order := []Scope{ScopeProject, ScopeUser}
	for _, scope := range order {
		path, ok := paths[scope]
		if !ok {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("contextfiles: read %s: %w", path, err)
		}
		out = append(out, File{Scope: scope, Path: path, Content: strings.TrimSpace(string(data))})
	}
	return out, nil
}

// RenderBlock serializes the loaded context files into a system-prompt section.
// An empty load yields "".
func RenderBlock(files []File) string {
	nonEmpty := make([]File, 0, len(files))
	for _, f := range files {
		if strings.TrimSpace(f.Content) != "" {
			nonEmpty = append(nonEmpty, f)
		}
	}
	if len(nonEmpty) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Project context\n")
	b.WriteString("Durable, evolving context the user and agent have recorded. Use it to inform decisions; it is softer than AGENTS.md rules.\n")
	for _, f := range nonEmpty {
		b.WriteString("\n### " + string(f.Scope) + " context (" + f.Path + ")\n")
		b.WriteString(f.Content)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// Append adds a line to a context file, creating it (and its parent dir) if
// needed. The line is normalized to a single trimmed string and written as a
// markdown bullet under a dated heading so entries stay scannable.
func Append(scope Scope, projectRoot string, userConfigDir string, line string) (string, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", errors.New("contextfiles: empty line")
	}
	paths := ResolvePaths(projectRoot, userConfigDir)
	path, ok := paths[scope]
	if !ok {
		return "", fmt.Errorf("contextfiles: no %s context path resolvable", scope)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("contextfiles: mkdir: %w", err)
	}
	existing := ""
	if data, err := os.ReadFile(path); err == nil {
		existing = string(data)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("contextfiles: read: %w", err)
	}
	var b strings.Builder
	b.WriteString(existing)
	if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n")
	}
	b.WriteString("- " + line + "\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return "", fmt.Errorf("contextfiles: write: %w", err)
	}
	return path, nil
}

// ListScopes returns the scopes that currently have a resolvable, existing file.
func ListScopes(projectRoot string, userConfigDir string) []Scope {
	paths := ResolvePaths(projectRoot, userConfigDir)
	out := []Scope{}
	for _, scope := range []Scope{ScopeProject, ScopeUser} {
		path, ok := paths[scope]
		if !ok {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			out = append(out, scope)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
