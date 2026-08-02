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
	"net/url"
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

const (
	sessionTimeout = 30 * time.Second
	sessionTTL     = 2 * time.Hour
)

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
	sess := NewSession(userAgent)
	s.sessions[key] = sess
	return sess
}

// Invalidate forgets a session — used when a tracker returns a login page
// where we expected real content.
//
// Do NOT use this to get a clean jar for validating a password. The store is
// keyed by (tracker, user), so one entry is shared by every one of that user's
// topics, and plugins re-resolve it per request — deleting it publishes an
// anonymous jar that a concurrent check can pick up mid-operation. Build an
// unstored session with NewSession, validate on that, and Put it once it is
// authenticated.
func (s *SessionStore) Invalidate(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, key)
}

// NewSession builds a session with its own cookie jar WITHOUT storing it, for
// work that must not observe or disturb the shared one — validating a
// password being the motivating case, since posting it onto an
// already-authenticated jar proves nothing about the password.
func NewSession(userAgent string) *Session {
	jar, _ := cookiejar.New(nil)
	return &Session{
		Client:    &http.Client{Jar: jar, Timeout: sessionTimeout},
		UserAgent: userAgent,
		ExpiresAt: time.Now().Add(sessionTTL),
	}
}

// Put installs a ready session under key in a single locked write, replacing
// any existing entry. Pair it with NewSession: publish only once the session
// is authenticated, so no reader can ever resolve an anonymous jar.
func (s *SessionStore) Put(key string, sess *Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[key] = sess
}

// SessionKey is the convention for building store keys.
func SessionKey(trackerName, userID string) string {
	return trackerName + ":" + userID
}

// CookiesByName returns the named cookies from the session jar for u as a
// name->value map. Names absent from the jar are simply omitted.
func CookiesByName(s *Session, u *url.URL, names []string) map[string]string {
	want := make(map[string]struct{}, len(names))
	for _, n := range names {
		want[n] = struct{}{}
	}
	out := map[string]string{}
	for _, c := range s.Client.Jar.Cookies(u) {
		if _, ok := want[c.Name]; ok {
			out[c.Name] = c.Value
		}
	}
	return out
}
