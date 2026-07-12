// Package kanban implements persistent task boards with cards, mirroring
// Mercury's Kanban boards. Boards and their cards are stored as JSON under the
// user config dir. The agent can process ("run") cards autonomously; each card
// becomes a prompt the agent executes, after which its status is advanced.
package kanban

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/BlusceLabs/green/internal/config"
)

// FileName is where boards are persisted under the config dir.
const FileName = "kanban.json"

// Status is a card's lifecycle state.
type Status string

const (
	StatusTodo    Status = "todo"
	StatusDoing   Status = "doing"
	StatusDone    Status = "done"
	StatusBlocked Status = "blocked"
)

// ValidStatus reports whether s is a known card status.
func ValidStatus(s string) bool {
	switch Status(s) {
	case StatusTodo, StatusDoing, StatusDone, StatusBlocked:
		return true
	}
	return false
}

// Priority orders card urgency.
type Priority string

const (
	PriorityLow  Priority = "low"
	PriorityMed  Priority = "med"
	PriorityHigh Priority = "high"
)

// Card is a single board item.
type Card struct {
	ID           string    `json:"id"`
	BoardID      string    `json:"boardId"`
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	Status       Status    `json:"status"`
	Priority     Priority  `json:"priority"`
	Labels       []string  `json:"labels"`
	Dependencies []string  `json:"dependencies"` // card IDs that must finish first
	Comments     []Comment `json:"comments"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// Comment is a note attached to a card (e.g. an agent run result).
type Comment struct {
	At      time.Time `json:"at"`
	Author  string    `json:"author"`
	Body    string    `json:"body"`
}

// Board groups cards.
type Board struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Cards     []Card    `json:"cards"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Store persists boards to a JSON file.
type Store struct {
	mu   sync.Mutex
	path string
}

// Open loads (or initializes) a store persisted at <configDir>/kanban.json.
// A nil/empty configDir uses an in-memory store.
func Open(configDir string) (*Store, error) {
	s := &Store{path: filepath.Join(configDir, FileName)}
	if configDir == "" {
		return s, nil
	}
	if _, err := os.Stat(s.path); os.IsNotExist(err) {
		return s, nil
	}
	return s, nil
}

func (s *Store) load() (map[string]*Board, error) {
	boards := map[string]*Board{}
	if s.path == "" {
		return boards, nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return boards, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return boards, nil
	}
	var list []Board
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse kanban store: %w", err)
	}
	for i := range list {
		b := list[i]
		boards[b.ID] = &b
	}
	return boards, nil
}

func (s *Store) save(boards map[string]*Board) error {
	if s.path == "" {
		return nil
	}
	list := make([]Board, 0, len(boards))
	for _, b := range boards {
		list = append(list, *b)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.Before(list[j].CreatedAt) })
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}

func newID(prefix string) string {
	return prefix + "-" + fmt.Sprintf("%d", time.Now().UnixNano())
}

// CreateBoard adds a board and returns it.
func (s *Store) CreateBoard(name string) (*Board, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	boards, err := s.load()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	b := &Board{ID: newID("board"), Name: name, CreatedAt: now, UpdatedAt: now}
	boards[b.ID] = b
	if err := s.save(boards); err != nil {
		return nil, err
	}
	return b, nil
}

// ListBoards returns all boards ordered by creation time.
func (s *Store) ListBoards() ([]*Board, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	boards, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]*Board, 0, len(boards))
	for _, b := range boards {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// GetBoard finds a board by id or name.
func (s *Store) GetBoard(ref string) (*Board, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	boards, err := s.load()
	if err != nil {
		return nil, err
	}
	if b, ok := boards[ref]; ok {
		return b, nil
	}
	for _, b := range boards {
		if b.Name == ref {
			return b, nil
		}
	}
	return nil, fmt.Errorf("board %q not found", ref)
}

// RemoveBoard deletes a board.
func (s *Store) RemoveBoard(ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	boards, err := s.load()
	if err != nil {
		return err
	}
	id := ref
	if _, ok := boards[id]; !ok {
		for _, b := range boards {
			if b.Name == ref {
				id = b.ID
				break
			}
		}
	}
	if _, ok := boards[id]; !ok {
		return fmt.Errorf("board %q not found", ref)
	}
	delete(boards, id)
	return s.save(boards)
}

// AddCard appends a card to a board.
func (s *Store) AddCard(boardRef, title, description string, priority Priority, labels, deps []string) (*Card, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	boards, err := s.load()
	if err != nil {
		return nil, err
	}
	board, err := findBoard(boards, boardRef)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if priority == "" {
		priority = PriorityMed
	}
	c := Card{
		ID:           newID("card"),
		BoardID:      board.ID,
		Title:        title,
		Description:  description,
		Status:       StatusTodo,
		Priority:     priority,
		Labels:       labels,
		Dependencies: deps,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	board.Cards = append(board.Cards, c)
	board.UpdatedAt = now
	boards[board.ID] = board
	if err := s.save(boards); err != nil {
		return nil, err
	}
	return &c, nil
}

// SetStatus changes a card's status.
func (s *Store) SetStatus(cardID string, status Status) (*Card, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	boards, err := s.load()
	if err != nil {
		return nil, err
	}
	card, board, err := findCard(boards, cardID)
	if err != nil {
		return nil, err
	}
	card.Status = status
	card.UpdatedAt = time.Now().UTC()
	board.UpdatedAt = card.UpdatedAt
	boards[board.ID] = board
	if err := s.save(boards); err != nil {
		return nil, err
	}
	return card, nil
}

// AddComment appends a comment to a card.
func (s *Store) AddComment(cardID, author, body string) (*Card, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	boards, err := s.load()
	if err != nil {
		return nil, err
	}
	card, board, err := findCard(boards, cardID)
	if err != nil {
		return nil, err
	}
	card.Comments = append(card.Comments, Comment{At: time.Now().UTC(), Author: author, Body: body})
	card.UpdatedAt = time.Now().UTC()
	board.UpdatedAt = card.UpdatedAt
	boards[board.ID] = board
	if err := s.save(boards); err != nil {
		return nil, err
	}
	return card, nil
}

// RemoveCard deletes a card by id.
func (s *Store) RemoveCard(cardID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	boards, err := s.load()
	if err != nil {
		return err
	}
	for _, b := range boards {
		for i, c := range b.Cards {
			if c.ID == cardID {
				b.Cards = append(b.Cards[:i], b.Cards[i+1:]...)
				b.UpdatedAt = time.Now().UTC()
				boards[b.ID] = b
				return s.save(boards)
			}
		}
	}
	return fmt.Errorf("card %q not found", cardID)
}

func findBoard(boards map[string]*Board, ref string) (*Board, error) {
	if b, ok := boards[ref]; ok {
		return b, nil
	}
	for _, b := range boards {
		if b.Name == ref {
			return b, nil
		}
	}
	return nil, fmt.Errorf("board %q not found", ref)
}

func findCard(boards map[string]*Board, cardID string) (*Card, *Board, error) {
	for _, b := range boards {
		for i := range b.Cards {
			if b.Cards[i].ID == cardID {
				return &b.Cards[i], b, nil
			}
		}
	}
	return nil, nil, fmt.Errorf("card %q not found", cardID)
}

// DefaultConfigDir returns the config dir for opening the default store.
func DefaultConfigDir() string {
	dir, err := config.UserConfigDir()
	if err != nil {
		return ""
	}
	return dir
}
