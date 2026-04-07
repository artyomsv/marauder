// Package forumcommon hosts shared helpers for forum-style tracker
// plugins. It is intentionally tiny: a session manager that holds an
// http.Client per (tracker_name, user_id) pair so concurrent topic
// checks can reuse the same login cookies.
//
// Plugins keep their per-tracker quirks (login form fields, topic
// selectors) in their own packages — this package only provides the
// generic plumbing.
package forumcommon

import (
	"net/http"
	"net/http/cookiejar"
	"sync"
	"time"
)

// SessionStore is a process-wide map of (tracker, user_id) -> *http.Client.
//
// Cookies are kept in memory only; if the process restarts, every plugin
// will need to log in again on its next check. This is OK for v0.3 — the
// alternative (persisting cookies in Postgres) is more invasive and is
// scheduled for v0.4.
type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

// Session is one logged-in HTTP client.
type Session struct {
	Client    *http.Client
	UserAgent string
	LoggedIn  bool
	ExpiresAt time.Time
}

// New constructs an empty store.
func New() *SessionStore {
	return &SessionStore{sessions: map[string]*Session{}}
}

// GetOrCreate returns the existing session for the key, or builds a fresh
// one with its own cookie jar.
func (s *SessionStore) GetOrCreate(key string, userAgent string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.sessions[key]; ok && time.Now().Before(existing.ExpiresAt) {
		return existing
	}
	jar, _ := cookiejar.New(nil)
	sess := &Session{
		Client: &http.Client{
			Jar:     jar,
			Timeout: 30 * time.Second,
		},
		UserAgent: userAgent,
		ExpiresAt: time.Now().Add(2 * time.Hour),
	}
	s.sessions[key] = sess
	return sess
}

// Invalidate forgets a session — used when a tracker returns a login page
// where we expected real content.
func (s *SessionStore) Invalidate(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, key)
}

// SessionKey is the convention for building store keys.
func SessionKey(trackerName, userID string) string {
	return trackerName + ":" + userID
}
