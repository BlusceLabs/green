// Package trajectory captures an agent session into a structured trajectory
// and compresses it for reuse — Hermes's "research-ready" feature. Captured
// trajectories feed fine-tuning and agent-eval datasets: they are the
// (prompt, tool-use, observation) sequences that teach or benchmark
// tool-calling models.
//
// Compression is deterministic and offline: it collapses redundant turns,
// truncates oversized tool outputs, and drops internal bookkeeping events
// (checkpoints, compactions) so the result is a clean prompt→action→result
// sequence without losing the decision points.
package trajectory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BlusceLabs/green/internal/sessions"
)

const schemaVersion = 1

// Step is one unit of a trajectory in chat-style (role/content) form.
type Step struct {
	Role     string `json:"role"` // "user", "assistant", "tool"
	Content  string `json:"content"`
	ToolName string `json:"toolName,omitempty"`
	// Truncated is true when Content was cut during compression.
	Truncated bool `json:"truncated,omitempty"`
}

// Trajectory is a captured + optionally compressed session.
type Trajectory struct {
	SchemaVersion int    `json:"schemaVersion"`
	SessionID     string `json:"sessionId"`
	GeneratedAt   string `json:"generatedAt"`
	Model         string `json:"model,omitempty"`
	Compressed    bool   `json:"compressed"`
	TokenEstimate int    `json:"tokenEstimate"`
	Steps         []Step `json:"steps"`
}

// Options tunes capture/compression.
type Options struct {
	// MaxToolOutput caps characters kept per tool result (0 = keep all).
	MaxToolOutput int
	// Now overrides the timestamp source (tests).
	Now func() time.Time
	// Model stamps the trajectory with the model that produced it.
	Model string
}

// metaEventTypes are internal bookkeeping events dropped during compression.
var metaEventTypes = map[sessions.EventType]bool{
	sessions.EventSessionCheckpoint: true,
	sessions.EventSessionRewind:     true,
	sessions.EventCompaction:        true,
	sessions.EventSessionFork:       true,
	sessions.EventSessionChild:      true,
}

// Capture reads a session's event log and builds a trajectory. tool results are
// truncated to opts.MaxToolOutput when set. A missing session is an error.
func Capture(store *sessions.Store, sessionID string, opts Options) (Trajectory, error) {
	if store == nil {
		store = sessions.NewStore(sessions.StoreOptions{})
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	meta, err := store.Get(sessionID)
	if err != nil {
		return Trajectory{}, err
	}
	if meta == nil {
		return Trajectory{}, fmt.Errorf("trajectory: session not found: %s", sessionID)
	}
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		return Trajectory{}, fmt.Errorf("trajectory: read events: %w", err)
	}
	steps := make([]Step, 0, len(events))
	for _, ev := range events {
		role, content, tool := decodeEvent(ev)
		if role == "" {
			continue
		}
		if ev.Type == sessions.EventToolResult && opts.MaxToolOutput > 0 && len(content) > opts.MaxToolOutput {
			content = strings.TrimSpace(content[:opts.MaxToolOutput]) + "…[truncated]"
			steps = append(steps, Step{Role: role, Content: content, ToolName: tool, Truncated: true})
			continue
		}
		steps = append(steps, Step{Role: role, Content: content, ToolName: tool})
	}
	traj := Trajectory{
		SchemaVersion: schemaVersion,
		SessionID:     sessionID,
		GeneratedAt:   now().UTC().Format(time.RFC3339),
		Model:         opts.Model,
		Steps:         steps,
	}
	traj.TokenEstimate = EstimateTokens(traj.Steps)
	return traj, nil
}

// Compress collapses redundant assistant turns, drops meta events, and applies
// the tool-output cap (idempotent — safe to call on already-compressed input).
// It returns a NEW trajectory; the receiver is unchanged.
func (t Trajectory) Compress(maxToolOutput int) Trajectory {
	out := Trajectory{
		SchemaVersion: t.SchemaVersion,
		SessionID:     t.SessionID,
		GeneratedAt:   t.GeneratedAt,
		Model:         t.Model,
		Compressed:    true,
	}
	steps := make([]Step, 0, len(t.Steps))
	for i, step := range t.Steps {
		// Drop internal bookkeeping.
		if step.Role == "meta" {
			continue
		}
		// Collapse a run of consecutive assistant messages: keep the last.
		if step.Role == "assistant" && i+1 < len(t.Steps) && t.Steps[i+1].Role == "assistant" {
			continue
		}
		s := step
		if s.Role == "tool" && maxToolOutput > 0 && len(s.Content) > maxToolOutput {
			s.Content = strings.TrimSpace(s.Content[:maxToolOutput]) + "…[truncated]"
			s.Truncated = true
		}
		steps = append(steps, s)
	}
	out.Steps = steps
	out.TokenEstimate = EstimateTokens(out.Steps)
	return out
}

// EstimateTokens approximates tokens as words / 0.75, the common heuristic for
// English-ish LLM token counts. It is a planning aid, not an exact bill.
func EstimateTokens(steps []Step) int {
	words := 0
	for _, s := range steps {
		words += len(strings.Fields(s.Content))
	}
	return int(float64(words) / 0.75)
}

// WriteJSON serializes the whole trajectory as a single JSON document.
func (t Trajectory) WriteJSON(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("trajectory: mkdir: %w", err)
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("trajectory: encode: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("trajectory: write: %w", err)
	}
	return nil
}

// WriteJSONL writes one JSON object per step (chat-style), suitable for
// line-oriented training pipelines.
func (t Trajectory) WriteJSONL(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("trajectory: mkdir: %w", err)
	}
	var b strings.Builder
	enc := json.NewEncoder(&b)
	for _, step := range t.Steps {
		if err := enc.Encode(step); err != nil {
			return fmt.Errorf("trajectory: encode step: %w", err)
		}
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("trajectory: write: %w", err)
	}
	return nil
}

// decodeEvent maps a session event to a trajectory step. Events without a
// usable role/content are returned with an empty role and skipped by Capture.
func decodeEvent(ev sessions.Event) (role string, content string, tool string) {
	var generic map[string]any
	if len(ev.Payload) > 0 {
		_ = json.Unmarshal(ev.Payload, &generic)
	}
	switch ev.Type {
	case sessions.EventMessage:
		role = stringOr(generic, "role", "user")
		content = stringOr(generic, "content", "")
	case sessions.EventToolCall:
		role = "assistant"
		tool = stringOr(generic, "name", stringOr(generic, "tool", ""))
		content = stringOr(generic, "arguments", stringOr(generic, "input", ""))
	case sessions.EventToolResult:
		role = "tool"
		tool = stringOr(generic, "name", stringOr(generic, "tool", ""))
		content = stringOr(generic, "content", stringOr(generic, "output", ""))
	case sessions.EventSpecDraft, sessions.EventSpecApproved, sessions.EventSpecRejected:
		role = "meta"
		content = string(ev.Type)
	default:
		role = "meta"
		content = string(ev.Type)
	}
	// Be forgiving: if content was nested as a non-string, stringify it.
	if content == "" && generic["content"] != nil {
		if s, ok := generic["content"].(string); ok {
			content = s
		}
	}
	return role, content, tool
}

func stringOr(m map[string]any, key string, fallback string) string {
	if m == nil {
		return fallback
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return fallback
}
