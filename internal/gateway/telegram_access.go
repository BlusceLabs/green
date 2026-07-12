package gateway

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/BlusceLabs/green/internal/config"
)

// telegramAccessFile is where approved/pending Telegram users are persisted
// under the user config dir.
const telegramAccessFile = "telegram-access.json"

// AccessRole is a Telegram user's privilege level.
type AccessRole string

const (
	// RoleAdmin can chat and manage other users (approve/reject/promote/...).
	RoleAdmin AccessRole = "admin"
	// RoleMember can chat only.
	RoleMember AccessRole = "member"
)

// AccessUser is an approved Telegram user.
type AccessUser struct {
	ID         string    `json:"id"`
	Username   string    `json:"username"`
	Role       AccessRole `json:"role"`
	ApprovedAt time.Time `json:"approvedAt"`
}

// PendingRequest is a not-yet-approved access request with its pairing code.
type PendingRequest struct {
	ID         string    `json:"id"`
	Username   string    `json:"username"`
	Code       string    `json:"code"`
	RequestedAt time.Time `json:"requestedAt"`
}

// AccessStore tracks Telegram admin/member access and pending requests. It is
// shared by the transport (which gates inbound messages) and the `green telegram`
// CLI (which approves/rejects requests). Persisted as JSON under the config dir.
type AccessStore struct {
	mu      sync.Mutex
	path    string
	Admins  map[string]AccessUser    `json:"admins"`
	Members map[string]AccessUser    `json:"members"`
	Pending map[string]PendingRequest `json:"pending"`
}

// LoadTelegramAccess loads (or initializes) the access store. A nil/empty
// configDir yields an in-memory store.
func LoadTelegramAccess(configDir string) (*AccessStore, error) {
	s := &AccessStore{
		path:    filepath.Join(configDir, telegramAccessFile),
		Admins:  map[string]AccessUser{},
		Members: map[string]AccessUser{},
		Pending: map[string]PendingRequest{},
	}
	if configDir == "" {
		return s, nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, s); err != nil {
			// Corrupt file: start fresh rather than blocking the bot.
			s.Admins = map[string]AccessUser{}
			s.Members = map[string]AccessUser{}
			s.Pending = map[string]PendingRequest{}
		}
	}
	if s.Admins == nil {
		s.Admins = map[string]AccessUser{}
	}
	if s.Members == nil {
		s.Members = map[string]AccessUser{}
	}
	if s.Pending == nil {
		s.Pending = map[string]PendingRequest{}
	}
	return s, nil
}

// DefaultTelegramAccessDir returns the config dir used for the default store.
func DefaultTelegramAccessDir() string {
	dir, err := config.UserConfigDir()
	if err != nil {
		return ""
	}
	return dir
}

func (s *AccessStore) save() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}

func (s *AccessStore) allApproved() map[string]AccessUser {
	out := map[string]AccessUser{}
	for k, v := range s.Admins {
		out[k] = v
	}
	for k, v := range s.Members {
		out[k] = v
	}
	return out
}

// IsApproved reports whether the user may chat.
func (s *AccessStore) IsApproved(userID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.Admins[userID]
	if ok {
		return true
	}
	_, ok = s.Members[userID]
	return ok
}

// IsPending reports whether the user has an outstanding request.
func (s *AccessStore) IsPending(userID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.Pending[userID]
	return ok
}

// Role returns the user's role ("" if unknown).
func (s *AccessStore) Role(userID string) AccessRole {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, ok := s.Admins[userID]; ok {
		return a.Role
	}
	if m, ok := s.Members[userID]; ok {
		return m.Role
	}
	return ""
}

// RequestAccess registers (or returns the existing code for) an access request.
// The first approved user becomes an admin automatically.
func (s *AccessStore) RequestAccess(username, userID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Admins[userID]; ok {
		return "", true
	}
	if _, ok := s.Members[userID]; ok {
		return "", true
	}
	if p, ok := s.Pending[userID]; ok {
		return p.Code, false
	}
	code := generateCode()
	s.Pending[userID] = PendingRequest{
		ID:         userID,
		Username:   username,
		Code:       code,
		RequestedAt: time.Now().UTC(),
	}
	_ = s.save()
	return code, false
}

// Approve approves a pending request by pairing code or user id, making the
// first approved user an admin.
func (s *AccessStore) Approve(ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	userID := s.findPending(ref)
	if userID == "" {
		return fmt.Errorf("no pending request matching %q", ref)
	}
	p := s.Pending[userID]
	delete(s.Pending, userID)
	role := RoleMember
	if len(s.Admins) == 0 && len(s.Members) == 0 {
		role = RoleAdmin // first user is the admin
	}
	target := s.Admins
	if role == RoleMember {
		target = s.Members
	}
	target[userID] = AccessUser{ID: userID, Username: p.Username, Role: role, ApprovedAt: time.Now().UTC()}
	_ = s.save()
	return nil
}

// Reject removes a pending request by code or user id.
func (s *AccessStore) Reject(ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	userID := s.findPending(ref)
	if userID == "" {
		return fmt.Errorf("no pending request matching %q", ref)
	}
	delete(s.Pending, userID)
	_ = s.save()
	return nil
}

// Remove revokes an approved user (admin or member) by user id or username.
func (s *AccessStore) Remove(ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.findApproved(ref)
	if id == "" {
		return fmt.Errorf("no approved user matching %q", ref)
	}
	delete(s.Admins, id)
	delete(s.Members, id)
	_ = s.save()
	return nil
}

// Promote makes an approved member an admin.
func (s *AccessStore) Promote(ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.findApproved(ref)
	if id == "" {
		return fmt.Errorf("no approved user matching %q", ref)
	}
	if m, ok := s.Members[id]; ok {
		m.Role = RoleAdmin
		delete(s.Members, id)
		s.Admins[id] = m
		_ = s.save()
	}
	return nil
}

// Demote makes an admin a member.
func (s *AccessStore) Demote(ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.findApproved(ref)
	if id == "" {
		return fmt.Errorf("no approved user matching %q", ref)
	}
	if a, ok := s.Admins[id]; ok {
		a.Role = RoleMember
		delete(s.Admins, id)
		s.Members[id] = a
		_ = s.save()
	}
	return nil
}

// Reset clears all access (admins, members, pending).
func (s *AccessStore) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Admins = map[string]AccessUser{}
	s.Members = map[string]AccessUser{}
	s.Pending = map[string]PendingRequest{}
	_ = s.save()
	return nil
}

// List returns approved users (admins first) and pending requests, ordered.
func (s *AccessStore) List() ([]AccessUser, []PendingRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var users []AccessUser
	for _, u := range s.Admins {
		users = append(users, u)
	}
	for _, u := range s.Members {
		users = append(users, u)
	}
	sort.Slice(users, func(i, j int) bool { return users[i].Username < users[j].Username })
	var pending []PendingRequest
	for _, p := range s.Pending {
		pending = append(pending, p)
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].Username < pending[j].Username })
	return users, pending
}

func (s *AccessStore) findPending(ref string) string {
	if p, ok := s.Pending[ref]; ok {
		return p.ID
	}
	for id, p := range s.Pending {
		if p.Code == ref || p.Username == ref {
			return id
		}
	}
	return ""
}

func (s *AccessStore) findApproved(ref string) string {
	if _, ok := s.Admins[ref]; ok {
		return ref
	}
	if _, ok := s.Members[ref]; ok {
		return ref
	}
	for id, u := range s.Admins {
		if u.Username == ref {
			return id
		}
	}
	for id, u := range s.Members {
		if u.Username == ref {
			return id
		}
	}
	return ""
}

func generateCode() string {
	n, err := rand.Int(rand.Reader, big.NewInt(900000))
	if err != nil {
		return fmt.Sprintf("%06d", time.Now().Nanosecond()%1000000)
	}
	return fmt.Sprintf("%06d", n.Int64()+100000)
}
