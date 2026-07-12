package learning

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

// randSuffix returns a short random hex string for unique id construction.
func randSuffix() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "0000"
	}
	return hex.EncodeToString(b)
}

// splitSentences is a lightweight sentence splitter good enough for memory
// extraction without pulling in a tokenizer.
func splitSentences(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	raw := strings.FieldsFunc(text, func(r rune) bool {
		return r == '.' || r == '!' || r == '?' || r == '\n'
	})
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if len(s) > 3 {
			out = append(out, s)
		}
	}
	return out
}
