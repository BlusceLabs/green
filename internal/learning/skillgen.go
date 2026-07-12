package learning

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// Turn is a single role/content unit of a conversation transcript.
type Turn struct {
	Role    string // "user", "assistant", "tool"
	Content string
}

// Transcript is the minimal conversation representation the learning loop
// consumes. It is intentionally decoupled from the sessions package so the
// loop can be exercised with synthetic input in tests.
type Transcript struct {
	SessionID string
	Turns     []Turn
}

// SkillDraft is a candidate reusable skill produced by the loop.
type SkillDraft struct {
	Name        string
	Description string
	Content     string
}

// ReflectReport summarizes what the learning loop did for a session.
type ReflectReport struct {
	MemoriesAdded   int      `json:"memoriesAdded"`
	ProfileFacts    int      `json:"profileFactsAdded"`
	SkillCreated    bool     `json:"skillCreated"`
	SkillName       string   `json:"skillName,omitempty"`
	NudgeMarked     bool     `json:"nudgeMarked"`
	ExtractedTopics []string `json:"extractedTopics,omitempty"`
}

// preferenceMarkers prefix phrases that strongly signal a durable preference.
var preferenceMarkers = []string{
	"i prefer", "i like", "i want", "i don't want", "i do not want",
	"always", "never", "please don't", "please do not", "avoid", "don't use",
	"do not use", "use only", "must", "should not", "only use",
}

// factMarkers prefix phrases that signal a project/technical fact.
var factMarkers = []string{
	"we use", "our project", "the repo", "the codebase", "this project",
	"the team", "our stack", "we run", "we deploy", "the build",
}

// Reflect runs the closed learning loop over a transcript: it curates memory,
// deepens the user profile, optionally mints a reusable skill, and nudges when
// the session was substantial but produced no durable knowledge.
func (s *Store) Reflect(t Transcript) (ReflectReport, error) {
	report := ReflectReport{}
	if s.Root == "" {
		return report, fmt.Errorf("learning: empty root directory")
	}

	var userText, assistantText strings.Builder
	toolCalls := 0
	for _, turn := range t.Turns {
		switch strings.ToLower(turn.Role) {
		case "user":
			userText.WriteString(turn.Content)
			userText.WriteString("\n")
		case "assistant":
			assistantText.WriteString(turn.Content)
			assistantText.WriteString("\n")
		case "tool":
			toolCalls++
			userText.WriteString(turn.Content)
			userText.WriteString("\n")
		}
	}

	// 1. Extract candidate memories from user turns.
	memories := extractMemories(userText.String())
	for _, m := range memories {
		if _, err := s.AppendMemory("", m.content, m.category, t.SessionID); err != nil {
			return report, err
		}
		report.MemoriesAdded++
	}

	// 2. Deepen the profile: topics from user text, facts from fact markers.
	topics := extractTopics(userText.String())
	facts := extractFacts(userText.String())
	if len(topics) > 0 || len(facts) > 0 {
		if _, err := s.UpdateProfile(facts, nil, topics); err != nil {
			return report, err
		}
		report.ProfileFacts = len(facts)
		report.ExtractedTopics = topics
	}

	// 3. Autonomous skill creation: a multi-step, successful-looking task with
	//    tool use is a good candidate to be captured as a reusable skill.
	if toolCalls >= 3 {
		draft, ok, err := s.ExtractSkill(t, s.Summarizer)
		if err != nil {
			return report, err
		}
		if ok {
			path, err := s.WriteSkill(draft)
			if err != nil {
				return report, err
			}
			_ = path
			report.SkillCreated = true
			report.SkillName = draft.Name
		}
	}

	// 4. Nudge: a substantial session that added no durable knowledge should
	//    prompt the user to persist something.
	durableWrite := report.MemoriesAdded > 0 || report.ProfileFacts > 0 ||
		len(report.ExtractedTopics) > 0 || report.SkillCreated
	if toolCalls >= 3 && !durableWrite {
		if err := s.MarkNudgePending(); err != nil {
			return report, err
		}
		report.NudgeMarked = true
	} else if durableWrite {
		// Any durable write is itself a knowledge checkpoint.
		if err := s.AcknowledgeNudge(); err != nil {
			return report, err
		}
	}

	return report, nil
}

type candidateMemory struct {
	content  string
	category MemoryCategory
}

// extractMemories scans free text for preference- and fact-like sentences and
// returns them as candidate memory entries.
func extractMemories(text string) []candidateMemory {
	var out []candidateMemory
	for _, sentence := range splitSentences(text) {
		lower := strings.ToLower(sentence)
		if hasMarker(lower, preferenceMarkers) {
			out = append(out, candidateMemory{content: sentence, category: CategoryPreference})
			continue
		}
		if hasMarker(lower, factMarkers) {
			out = append(out, candidateMemory{content: sentence, category: CategoryProject})
		}
	}
	return out
}

func extractFacts(text string) []string {
	var out []string
	for _, sentence := range splitSentences(text) {
		if hasMarker(strings.ToLower(sentence), factMarkers) {
			out = append(out, sentence)
		}
	}
	return out
}

// extractTopics pulls noun-ish phrases from user text. The heuristic keeps
// capitalized tokens and quoted/backticked identifiers as topic signals.
func extractTopics(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		for _, word := range strings.Fields(line) {
			word = strings.Trim(word, "`\"'.,:;()")
			if len(word) < 3 {
				continue
			}
			r := []rune(word)
			if !unicode.IsUpper(r[0]) {
				continue
			}
			if seen[word] {
				continue
			}
			seen[word] = true
			out = append(out, word)
		}
	}
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

func hasMarker(lower string, markers []string) bool {
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// taskVerbs are leading phrases that mark a request as an actionable task worth
// capturing as a reusable skill. Casual chat ("just chatting about the weather")
// does not contain them, so the loop won't mint a skill for small talk.
var taskVerbs = []string{
	"set up", "create", "add", "fix", "build", "write", "implement", "run",
	"configure", "deploy", "refactor", "update", "generate", "make", "install",
	"remove", "migrate", "test", "debug", "optimize", "document", "integrate",
}

// ExtractSkill produces a reusable skill draft from a transcript. When a
// Summarizer is provided it refines the description; otherwise a deterministic
// description is used. It returns ok=false when there is nothing skill-worthy
// (e.g. casual chat with no actionable task).
func (s *Store) ExtractSkill(t Transcript, summarize Summarizer) (SkillDraft, bool, error) {
	userPrompt := firstUserTurn(t)
	if strings.TrimSpace(userPrompt) == "" {
		return SkillDraft{}, false, nil
	}
	lower := strings.ToLower(userPrompt)
	isTask := false
	for _, verb := range taskVerbs {
		if strings.Contains(lower, verb) {
			isTask = true
			break
		}
	}
	if !isTask {
		return SkillDraft{}, false, nil
	}
	name := skillNameFromPrompt(userPrompt)
	description := fmt.Sprintf("Auto-generated skill from session %s: %s", shortID(t.SessionID), summarizeText(userPrompt, 80))
	if summarize != nil {
		if refined, err := summarize(userPrompt); err == nil && strings.TrimSpace(refined) != "" {
			description = strings.TrimSpace(refined)
		}
	}
	content := buildSkillContent(userPrompt, t)
	draft := SkillDraft{Name: name, Description: description, Content: content}
	return draft, true, nil
}

// WriteSkill materializes a draft as a SKILL.md under the store's SkillsDir,
// returning the written path. Name collisions are disambiguated deterministically.
func (s *Store) WriteSkill(d SkillDraft) (string, error) {
	if err := s.Ensure(); err != nil {
		return "", err
	}
	name := sanitizeSkillName(d.Name)
	if name == "" {
		name = "auto-skill"
	}
	dir := filepath.Join(s.SkillsDir, name)
	if _, err := os.Stat(dir); err == nil {
		dir = filepath.Join(s.SkillsDir, name+"-"+randSuffix())
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("learning: create skill dir: %w", err)
	}
	body := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", name, d.Description, d.Content)
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return "", fmt.Errorf("learning: write skill: %w", err)
	}
	return path, nil
}

func firstUserTurn(t Transcript) string {
	for _, turn := range t.Turns {
		if strings.EqualFold(turn.Role, "user") {
			return turn.Content
		}
	}
	return ""
}

func skillNameFromPrompt(prompt string) string {
	words := strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return r
		}
		return ' '
	}, prompt))
	if len(words) == 0 {
		return "auto-skill"
	}
	limit := 4
	if len(words) < limit {
		limit = len(words)
	}
	name := strings.ToLower(strings.Join(words[:limit], "-"))
	return sanitizeSkillName(name)
}

func sanitizeSkillName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '-' {
			return r
		}
		return '-'
	}, strings.ToLower(name))
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}
	return strings.Trim(name, "-")
}

func buildSkillContent(userPrompt string, t Transcript) string {
	var b strings.Builder
	b.WriteString("## When to use\n")
	b.WriteString("This skill was learned from a prior task. Use it when a request resembles:\n\n")
	b.WriteString("> " + summarizeText(userPrompt, 200) + "\n\n")
	b.WriteString("## Approach\n")
	b.WriteString("The original task involved the following steps and tools. Adapt them to the new context:\n\n")
	steps := 0
	for _, turn := range t.Turns {
		if strings.EqualFold(turn.Role, "tool") {
			summary := summarizeText(turn.Content, 120)
			if summary == "" {
				continue
			}
			b.WriteString("- " + summary + "\n")
			steps++
			if steps >= 20 {
				break
			}
		}
	}
	if steps == 0 {
		b.WriteString("- (no tool activity recorded)\n")
	}
	return b.String()
}

func summarizeText(text string, max int) string {
	text = strings.TrimSpace(text)
	if len(text) <= max {
		return text
	}
	return strings.TrimSpace(text[:max]) + "…"
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

var _ = time.Now
