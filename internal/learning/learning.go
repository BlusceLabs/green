// Package learning implements green's self-improving "learning loop" — the
// signature behaviour of Nous Research's Hermes Agent. It is the closed feedback
// cycle that lets an agent grow across sessions:
//
//   - Agent-curated MEMORY: durable knowledge (facts, preferences, procedures,
//     feedback) the agent persists so it does not re-learn the same thing.
//   - A deepening USER PROFILE: a model of who you are that accrues facts,
//     preferences, and topics of interest as you work together.
//   - Autonomous SKILL CREATION: after a complex task, the loop can mint a new
//     reusable skill from the transcript so the next similar task is cheaper.
//   - Periodic NUDGES: the agent reminds itself (and you) to persist knowledge
//     when it has been a while since the last memory write.
//   - Cross-session RECALL: past sessions can be searched and summarized so the
//     agent reuses what it already knows instead of starting cold.
//
// The package is intentionally dependency-free and works fully offline: the
// "intelligence" (turning a transcript into a memory or a skill) uses a
// deterministic heuristic by default, but every step accepts an optional
// Summarizer hook so a caller can plug in an LLM for higher-quality extraction.
package learning

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MemoryCategory classifies a memory entry so the recall prompt can be scoped.
type MemoryCategory string

const (
	CategoryFact       MemoryCategory = "fact"
	CategoryPreference MemoryCategory = "preference"
	CategoryProcedure  MemoryCategory = "procedure"
	CategoryFeedback   MemoryCategory = "feedback"
	CategoryProject    MemoryCategory = "project"
)

// AllCategories is the canonical ordered list, used for listing and validation.
var AllCategories = []MemoryCategory{
	CategoryFact,
	CategoryPreference,
	CategoryProcedure,
	CategoryFeedback,
	CategoryProject,
}

// MemoryEntry is a single durable knowledge record.
type MemoryEntry struct {
	ID         string         `json:"id"`
	Content    string         `json:"content"`
	Category   MemoryCategory `json:"category"`
	CreatedAt  time.Time      `json:"createdAt"`
	UpdatedAt  time.Time      `json:"updatedAt"`
	Source     string         `json:"source,omitempty"`     // session id, "nudge", or "user"
	Confidence float64        `json:"confidence,omitempty"` // 0..1
}

// Profile is the agent's evolving model of the user.
type Profile struct {
	Facts       []string  `json:"facts"`
	Preferences []string  `json:"preferences"`
	Topics      []string  `json:"topics"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// NudgeState tracks the periodic "persist your knowledge" reminder.
type NudgeState struct {
	LastNudge time.Time `json:"lastNudge"`
	Pending   bool      `json:"pending"`
}

// Summarizer turns raw text (a transcript excerpt, a memory draft) into a
// refined string. A nil Summarizer makes the loop use deterministic fallbacks.
type Summarizer func(text string) (string, error)

// Store is the persistence surface for the learning loop. All writes go under a
// single root directory (mirroring skills.DefaultDir / sessions.DefaultRoot).
type Store struct {
	Root string
	Now  func() time.Time
	// Summarizer, when set, upgrades the quality of generated memory/skills.
	Summarizer Summarizer
	// SkillsDir is where autonomously-created skills are written (a skills root).
	SkillsDir string
}

const (
	memoriesFile = "memories.json"
	profileFile  = "profile.json"
	nudgeFile    = "nudge.json"

	defaultNudgeInterval = 24 * time.Hour
)

// DefaultDir resolves the learning root: explicit GREEN_LEARNING_DIR wins; else
// $XDG_DATA_HOME/green/learning or ~/.local/share/green/learning. The directory
// is NOT created — callers that want persistence must call Ensure().
func DefaultDir(env map[string]string) string {
	if override := strings.TrimSpace(envValue(env, "GREEN_LEARNING_DIR")); override != "" {
		return override
	}
	dataHome := strings.TrimSpace(envValue(env, "XDG_DATA_HOME"))
	home := strings.TrimSpace(envValue(env, "HOME"))
	if home == "" {
		if userHome, err := os.UserHomeDir(); err == nil {
			home = userHome
		}
	}
	base := dataHome
	if base == "" {
		if home == "" {
			return ""
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "green", "learning")
}

// NewStore builds a Store rooted at root. A blank root falls back to DefaultDir.
func NewStore(root string) *Store {
	if strings.TrimSpace(root) == "" {
		root = DefaultDir(nil)
	}
	now := time.Now
	return &Store{
		Root:      root,
		Now:       now,
		SkillsDir: filepath.Join(root, "skills"),
	}
}

// Ensure creates the root (and the skills subdirectory) so the store is ready
// to persist. It is safe to call repeatedly.
func (s *Store) Ensure() error {
	if s.Root == "" {
		return errors.New("learning: empty root directory")
	}
	if err := os.MkdirAll(s.Root, 0o700); err != nil {
		return fmt.Errorf("learning: create root: %w", err)
	}
	if err := os.MkdirAll(s.SkillsDir, 0o700); err != nil {
		return fmt.Errorf("learning: create skills dir: %w", err)
	}
	return nil
}

// --- Memory -----------------------------------------------------------------

// AppendMemory writes a memory entry. When id is empty a new one is created
// (Content required); when id matches an existing entry it is updated in place
// (Content may be empty to leave unchanged). Returns the resulting entry.
func (s *Store) AppendMemory(id string, content string, category MemoryCategory, source string) (MemoryEntry, error) {
	if s.Root == "" {
		return MemoryEntry{}, errors.New("learning: empty root directory")
	}
	memories, err := s.readMemories()
	if err != nil {
		return MemoryEntry{}, err
	}
	now := s.Now()
	if strings.TrimSpace(id) == "" {
		content = strings.TrimSpace(content)
		if content == "" {
			return MemoryEntry{}, errors.New("learning: memory content is required")
		}
		entry := MemoryEntry{
			ID:         newID(now),
			Content:    content,
			Category:   normalizeCategory(category),
			CreatedAt:  now,
			UpdatedAt:  now,
			Source:     source,
			Confidence: 1.0,
		}
		memories = append(memories, entry)
		if err := s.writeMemories(memories); err != nil {
			return MemoryEntry{}, err
		}
		return entry, nil
	}
	idx := -1
	for i, m := range memories {
		if m.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return MemoryEntry{}, fmt.Errorf("learning: memory %q not found", id)
	}
	if content != "" {
		memories[idx].Content = strings.TrimSpace(content)
	}
	if category != "" {
		memories[idx].Category = normalizeCategory(category)
	}
	memories[idx].UpdatedAt = now
	if source != "" {
		memories[idx].Source = source
	}
	if err := s.writeMemories(memories); err != nil {
		return MemoryEntry{}, err
	}
	return memories[idx], nil
}

// ListMemories returns all memory entries sorted by most-recently-updated. When
// category is non-empty it filters to that category.
func (s *Store) ListMemories(category MemoryCategory) ([]MemoryEntry, error) {
	memories, err := s.readMemories()
	if err != nil {
		return nil, err
	}
	out := make([]MemoryEntry, 0, len(memories))
	for _, m := range memories {
		if category != "" && m.Category != category {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

// RemoveMemory deletes a memory by id. It returns true if something was removed.
func (s *Store) RemoveMemory(id string) (bool, error) {
	memories, err := s.readMemories()
	if err != nil {
		return false, err
	}
	for i, m := range memories {
		if m.ID == id {
			memories = append(memories[:i], memories[i+1:]...)
			return true, s.writeMemories(memories)
		}
	}
	return false, nil
}

// RenderMemoryBlock serializes memories into a markdown block suitable for
// injecting into the system prompt. An empty store yields "".
func (s *Store) RenderMemoryBlock() (string, error) {
	memories, err := s.ListMemories("")
	if err != nil {
		return "", err
	}
	if len(memories) == 0 {
		return "", nil
	}
	byCat := map[MemoryCategory][]string{}
	order := []MemoryCategory{}
	for _, m := range memories {
		if _, ok := byCat[m.Category]; !ok {
			byCat[m.Category] = []string{}
			order = append(order, m.Category)
		}
		byCat[m.Category] = append(byCat[m.Category], "- "+m.Content)
	}
	var b strings.Builder
	b.WriteString("## Persistent memory\n")
	b.WriteString("The agent has learned the following across past sessions. Honor it unless the user revises it.\n")
	for _, cat := range order {
		b.WriteString("\n### " + string(cat) + "s\n")
		b.WriteString(strings.Join(byCat[cat], "\n"))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String()), nil
}

func (s *Store) readMemories() ([]MemoryEntry, error) {
	if s.Root == "" {
		return []MemoryEntry{}, nil
	}
	data, err := os.ReadFile(filepath.Join(s.Root, memoriesFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []MemoryEntry{}, nil
		}
		return nil, fmt.Errorf("learning: read memories: %w", err)
	}
	var out []MemoryEntry
	if err := json.Unmarshal(data, &out); err != nil {
		// A corrupt memory file must not block the agent; start fresh.
		return []MemoryEntry{}, nil
	}
	return out, nil
}

func (s *Store) writeMemories(memories []MemoryEntry) error {
	if err := s.Ensure(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(memories, "", "  ")
	if err != nil {
		return fmt.Errorf("learning: encode memories: %w", err)
	}
	if err := os.WriteFile(filepath.Join(s.Root, memoriesFile), append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("learning: write memories: %w", err)
	}
	return nil
}

// --- Profile ----------------------------------------------------------------

// GetProfile returns the current user profile (empty if none).
func (s *Store) GetProfile() (Profile, error) {
	if s.Root == "" {
		return Profile{}, nil
	}
	data, err := os.ReadFile(filepath.Join(s.Root, profileFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Profile{}, nil
		}
		return Profile{}, fmt.Errorf("learning: read profile: %w", err)
	}
	var p Profile
	if err := json.Unmarshal(data, &p); err != nil {
		return Profile{}, nil
	}
	return p, nil
}

// UpdateProfile merges new facts/preferences/topics into the profile,
// de-duplicating within each list. Passing nil slices leaves that list intact.
func (s *Store) UpdateProfile(addFacts, addPrefs, addTopics []string) (Profile, error) {
	if err := s.Ensure(); err != nil {
		return Profile{}, err
	}
	p, err := s.GetProfile()
	if err != nil {
		return Profile{}, err
	}
	p.Facts = mergeUnique(p.Facts, addFacts)
	p.Preferences = mergeUnique(p.Preferences, addPrefs)
	p.Topics = mergeUnique(p.Topics, addTopics)
	p.UpdatedAt = s.Now()
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return Profile{}, fmt.Errorf("learning: encode profile: %w", err)
	}
	if err := os.WriteFile(filepath.Join(s.Root, profileFile), append(data, '\n'), 0o600); err != nil {
		return Profile{}, fmt.Errorf("learning: write profile: %w", err)
	}
	return p, nil
}

// RenderProfileBlock serializes the user profile for the system prompt.
func (s *Store) RenderProfileBlock() (string, error) {
	p, err := s.GetProfile()
	if err != nil {
		return "", err
	}
	if len(p.Facts) == 0 && len(p.Preferences) == 0 && len(p.Topics) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("## User profile\n")
	b.WriteString("What the agent knows about you (deepening across sessions):\n")
	if len(p.Facts) > 0 {
		b.WriteString("\n### Facts\n" + strings.Join(prefix("- ", p.Facts), "\n") + "\n")
	}
	if len(p.Preferences) > 0 {
		b.WriteString("\n### Preferences\n" + strings.Join(prefix("- ", p.Preferences), "\n") + "\n")
	}
	if len(p.Topics) > 0 {
		b.WriteString("\n### Topics of interest\n" + strings.Join(prefix("- ", p.Topics), "\n") + "\n")
	}
	return strings.TrimSpace(b.String()), nil
}

// --- Nudge ------------------------------------------------------------------

// NudgeInterval is how long the loop waits before suggesting a knowledge
// checkpoint. Exposed so callers/tests can tune it.
var NudgeInterval = defaultNudgeInterval

// NudgeStatus reports whether a nudge is due and the last checkpoint time.
type NudgeStatus struct {
	Pending   bool      `json:"pending"`
	LastNudge time.Time `json:"lastNudge"`
	NextNudge time.Time `json:"nextNudge"`
	Due       bool      `json:"due"`
}

// Nudge returns the current nudge status.
func (s *Store) Nudge() (NudgeStatus, error) {
	state, err := s.readNudge()
	if err != nil {
		return NudgeStatus{}, err
	}
	now := s.Now()
	next := state.LastNudge.Add(NudgeInterval)
	return NudgeStatus{
		Pending:   state.Pending,
		LastNudge: state.LastNudge,
		NextNudge: next,
		Due:       now.After(next),
	}, nil
}

// MarkNudgePending flips the nudge to pending (e.g. after a long session with
// no memory writes). It does not change LastNudge.
func (s *Store) MarkNudgePending() error {
	state, err := s.readNudge()
	if err != nil {
		return err
	}
	state.Pending = true
	return s.writeNudge(state)
}

// AcknowledgeNudge records that the agent/user persisted knowledge, clearing
// the pending flag and refreshing LastNudge.
func (s *Store) AcknowledgeNudge() error {
	state := NudgeState{Pending: false, LastNudge: s.Now()}
	return s.writeNudge(state)
}

func (s *Store) readNudge() (NudgeState, error) {
	if s.Root == "" {
		return NudgeState{}, nil
	}
	data, err := os.ReadFile(filepath.Join(s.Root, nudgeFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return NudgeState{}, nil
		}
		return NudgeState{}, fmt.Errorf("learning: read nudge: %w", err)
	}
	var state NudgeState
	if err := json.Unmarshal(data, &state); err != nil {
		return NudgeState{}, nil
	}
	return state, nil
}

func (s *Store) writeNudge(state NudgeState) error {
	if err := s.Ensure(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("learning: encode nudge: %w", err)
	}
	if err := os.WriteFile(filepath.Join(s.Root, nudgeFile), append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("learning: write nudge: %w", err)
	}
	return nil
}

// --- helpers ----------------------------------------------------------------

func normalizeCategory(c MemoryCategory) MemoryCategory {
	if c == "" {
		return CategoryFact
	}
	for _, known := range AllCategories {
		if c == known {
			return c
		}
	}
	return CategoryFact
}

func mergeUnique(existing []string, add []string) []string {
	// The dedup key is lower-cased so "Go" and "go" collapse to the first-seen
	// casing, which matters for uppercase topic tokens extracted from prose.
	seen := map[string]bool{}
	out := make([]string, 0, len(existing)+len(add))
	for _, v := range existing {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		key := strings.ToLower(v)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, v)
	}
	for _, v := range add {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		key := strings.ToLower(v)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, v)
	}
	return out
}

func prefix(p string, items []string) []string {
	out := make([]string, len(items))
	for i, v := range items {
		out[i] = p + v
	}
	return out
}

func newID(t time.Time) string {
	return t.UTC().Format("20060102T150405") + "-" + randSuffix()
}

func envValue(env map[string]string, key string) string {
	if env != nil {
		return env[key]
	}
	return os.Getenv(key)
}
