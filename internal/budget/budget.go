// Package budget tracks per-day LLM token spend so an operator can cap a
// session's cost, mirroring Mercury's daily token budget. Usage is recorded
// from the agent loop's OnUsage callback and persisted as JSON under the user
// config dir. A limit of 0 means "unlimited" (no enforcement).
package budget

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/BlusceLabs/green/internal/greenruntime"
)

// FileName is where daily usage is persisted under the config dir.
const FileName = "token-usage.json"

// DayEntry is the recorded usage for a single UTC calendar day.
type DayEntry struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
}

// persisted is the on-disk shape of a Tracker: the configured limit/override
// plus the per-day usage history.
type persisted struct {
	Limit    int                  `json:"limit"`
	Override bool                 `json:"override"`
	Days     map[string]DayEntry  `json:"days"`
}

// totals returns the combined token count for the day.
func (d DayEntry) totals() int { return d.InputTokens + d.OutputTokens }

// Tracker records and enforces a daily token budget.
type Tracker struct {
	mu sync.Mutex

	path  string
	limit int // 0 == unlimited
	override bool

	days map[string]DayEntry // keyed by UTC YYYY-MM-DD
}

// New loads (or initializes) a tracker persisted at <configDir>/token-usage.json.
// A nil/empty configDir yields an in-memory tracker (no persistence).
func New(configDir string) (*Tracker, error) {
	t := &Tracker{
		path: filepath.Join(configDir, FileName),
		days: map[string]DayEntry{},
	}
	if configDir == "" {
		return t, nil
	}
	data, err := os.ReadFile(t.path)
	if err != nil {
		if os.IsNotExist(err) {
			return t, nil
		}
		return nil, fmt.Errorf("read token usage: %w", err)
	}
	if len(data) > 0 {
		var p persisted
		if err := json.Unmarshal(data, &p); err != nil {
			// A corrupt file shouldn't block the agent; start fresh.
			t.days = map[string]DayEntry{}
		} else {
			t.limit = p.Limit
			t.override = p.Override
			t.days = p.Days
			if t.days == nil {
				t.days = map[string]DayEntry{}
			}
		}
	}
	return t, nil
}

func today() string { return time.Now().UTC().Format("2006-01-02") }

// Record adds the tokens from a usage event to today's entry and persists.
func (t *Tracker) Record(u greenruntime.Usage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	d := t.days[today()]
	d.InputTokens += u.InputTokens + u.PromptTokens + u.CachedInputTokens + u.CacheWriteTokens
	d.OutputTokens += u.OutputTokens + u.CompletionTokens + u.ReasoningTokens
	t.days[today()] = d
	t.pruneLocked()
	t.saveLocked()
}

// Status reports the current budget state for today.
type Status struct {
	Date     string `json:"date"`
	Used     int    `json:"used"`
	Limit    int    `json:"limit"` // 0 == unlimited
	Remaining int   `json:"remaining"`
	Over     bool   `json:"over"`
	Override bool   `json:"override"`
}

// Status returns today's budget status.
func (t *Tracker) Status() Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	used := t.days[today()].totals()
	remaining := 0
	over := false
	if t.limit > 0 {
		remaining = t.limit - used
		if remaining < 0 {
			remaining = 0
		}
		over = used > t.limit
	}
	return Status{
		Date:      today(),
		Used:      used,
		Limit:     t.limit,
		Remaining: remaining,
		Over:      over,
		Override:  t.override,
	}
}

// Exceeded reports whether today's spend is over the limit and not overridden.
func (t *Tracker) Exceeded() bool {
	s := t.Status()
	return s.Limit > 0 && s.Over && !s.Override
}

// SetLimit changes the daily limit (0 disables enforcement) and persists.
func (t *Tracker) SetLimit(n int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if n < 0 {
		return fmt.Errorf("limit must be >= 0")
	}
	t.limit = n
	return t.saveLocked()
}

// Limit returns the configured daily limit (0 == unlimited).
func (t *Tracker) Limit() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.limit
}

// SetOverride toggles the one-session override that lets the agent keep going
// past the limit, and persists it.
func (t *Tracker) SetOverride(on bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.override = on
	return t.saveLocked()
}

// Override reports whether the over-limit override is currently active.
func (t *Tracker) Override() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.override
}

// Reset zeroes today's usage (and the override) and persists.
func (t *Tracker) Reset() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.days[today()] = DayEntry{}
	t.override = false
	t.pruneLocked()
	return t.saveLocked()
}

// pruneLocked drops entries older than 7 days. Caller holds mu.
func (t *Tracker) pruneLocked() {
	cutoff := time.Now().UTC().Add(-7 * 24 * time.Hour).Format("2006-01-02")
	for k := range t.days {
		if k < cutoff {
			delete(t.days, k)
		}
	}
}

// saveLocked persists the tracker. Caller holds mu.
func (t *Tracker) saveLocked() error {
	if t.path == "" {
		return nil
	}
	p := persisted{
		Limit:    t.limit,
		Override: t.override,
		Days:     t.days,
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(t.path, data, 0o600)
}
