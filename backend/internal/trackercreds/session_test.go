package trackercreds

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// sessionTracker models a tracker whose session lives in the plugin, keyed by
// user only, the way every forum plugin's SessionStore does. Verify reports
// whether that session is signed in — as ANY account, which is exactly what the
// real plugins check, and what makes a credential edit dangerous. Login blocks
// on release (or its context), so a test can hold one attempt open.
type sessionTracker struct {
	plainTracker
	release chan struct{}

	mu      sync.Mutex
	account map[uuid.UUID]string // user → account the session is signed in as
	logins  []string             // usernames Login was called with, in order

	loginCalls  atomic.Int32
	verifyCalls atomic.Int32
	inFlight    atomic.Int32
	maxInFlight atomic.Int32
	loginStart  chan struct{}
}

func newSessionTracker() *sessionTracker {
	return &sessionTracker{
		release:    make(chan struct{}),
		account:    map[uuid.UUID]string{},
		loginStart: make(chan struct{}, 64),
	}
}

func (s *sessionTracker) Name() string { return "session-test" }

func (s *sessionTracker) enter() func() {
	n := s.inFlight.Add(1)
	for {
		m := s.maxInFlight.Load()
		if n <= m || s.maxInFlight.CompareAndSwap(m, n) {
			break
		}
	}
	return func() { s.inFlight.Add(-1) }
}

func (s *sessionTracker) Verify(ctx context.Context, c *domain.TrackerCredential) (bool, error) {
	defer s.enter()()
	s.verifyCalls.Add(1)
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.account[c.UserID] != "", nil
}

func (s *sessionTracker) Login(ctx context.Context, c *domain.TrackerCredential) error {
	defer s.enter()()
	s.loginCalls.Add(1)
	s.mu.Lock()
	s.logins = append(s.logins, c.Username)
	s.mu.Unlock()
	s.loginStart <- struct{}{}
	select {
	case <-s.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.mu.Lock()
	s.account[c.UserID] = c.Username
	s.mu.Unlock()
	return nil
}

func (s *sessionTracker) waitLogins(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-s.loginStart:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of %d Login calls started", i, n)
		}
	}
}

func sessionCred(user uuid.UUID, username string) *domain.TrackerCredential {
	return &domain.TrackerCredential{
		ID: uuid.New(), UserID: user, TrackerName: "session-test",
		Username: username, SecretEnc: []byte("hunter2"),
	}
}

// TestEnsureSession_ConcurrentCallers_OneLoginNoOverlap is issue #198: the
// scheduler's workers check one user's topics at the same moment, and each
// sent its own login. Tapochek answered four of them with 503. However the
// callers interleave, one login must serve them all and no two requests for
// that session may be in flight together.
func TestEnsureSession_ConcurrentCallers_OneLoginNoOverlap(t *testing.T) {
	tr := newSessionTracker()
	close(tr.release)
	user := uuid.New()
	const callers = 8

	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- EnsureSession(context.Background(), tr, sessionCred(user, "alice"))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("caller: %v", err)
		}
	}
	if got := tr.loginCalls.Load(); got != 1 {
		t.Errorf("login calls = %d, want 1 — concurrent checks of one user's "+
			"topics must share a single login", got)
	}
	if got := tr.maxInFlight.Load(); got != 1 {
		t.Errorf("max concurrent tracker requests = %d, want 1", got)
	}
}

// TestEnsureSession_LiveSession_SkipsLogin: once a session exists, a check
// costs one Verify and no login at all.
func TestEnsureSession_LiveSession_SkipsLogin(t *testing.T) {
	tr := newSessionTracker()
	close(tr.release)
	user := uuid.New()

	for i := 0; i < 3; i++ {
		if err := EnsureSession(context.Background(), tr, sessionCred(user, "alice")); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if got := tr.loginCalls.Load(); got != 1 {
		t.Errorf("login calls = %d, want 1 (the cold start only)", got)
	}
	if got := tr.verifyCalls.Load(); got != 2 {
		t.Errorf("verify calls = %d, want 2 (every call after the login)", got)
	}
}

// TestEnsureSession_ChangedAccount_LogsInAgain: the plugin's session is keyed
// by Marauder user, not by tracker account, and Verify only asks "is this
// session signed in". So after the user edits the stored username, the old
// account's session still verifies — trusting it would keep checking and
// downloading as the previous account until the session expired.
func TestEnsureSession_ChangedAccount_LogsInAgain(t *testing.T) {
	tr := newSessionTracker()
	close(tr.release)
	user := uuid.New()

	if err := EnsureSession(context.Background(), tr, sessionCred(user, "alice")); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSession(context.Background(), tr, sessionCred(user, "bob")); err != nil {
		t.Fatal(err)
	}
	if got := tr.account[user]; got != "bob" {
		t.Errorf("session signed in as %q, want %q — an edited credential must "+
			"not reuse the previous account's session", got, "bob")
	}
}

// TestEnsureSession_EditDuringLogin_NewCredentialLogsInItself: a credential
// edit that lands between two workers' reads gives them different credentials
// for one session. The worker holding the new one must not be handed the old
// login's success, and the old login must not end up owning the session.
func TestEnsureSession_EditDuringLogin_NewCredentialLogsInItself(t *testing.T) {
	tr := newSessionTracker()
	user := uuid.New()

	oldDone := make(chan error, 1)
	go func() { oldDone <- EnsureSession(context.Background(), tr, sessionCred(user, "alice")) }()
	tr.waitLogins(t, 1)

	newDone := make(chan error, 1)
	go func() { newDone <- EnsureSession(context.Background(), tr, sessionCred(user, "bob")) }()

	close(tr.release)
	if err := <-oldDone; err != nil {
		t.Fatalf("old credential: %v", err)
	}
	if err := <-newDone; err != nil {
		t.Fatalf("new credential: %v", err)
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if got := tr.account[user]; got != "bob" {
		t.Errorf("session signed in as %q, want %q (logins: %v)", got, "bob", tr.logins)
	}
}

// TestEnsureSession_ShortCallerDoesNotSpendALongerCallersBudget: callers carry
// very different budgets — the AddTopic preview warms for 5s, a scheduler
// check has 35s. A preview that starts first and runs out must not make the
// scheduler's check fail on the preview's deadline.
func TestEnsureSession_ShortCallerDoesNotSpendALongerCallersBudget(t *testing.T) {
	tr := newSessionTracker()
	user := uuid.New()

	shortCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	shortDone := make(chan error, 1)
	go func() { shortDone <- EnsureSession(shortCtx, tr, sessionCred(user, "alice")) }()
	tr.waitLogins(t, 1)

	longDone := make(chan error, 1)
	go func() { longDone <- EnsureSession(context.Background(), tr, sessionCred(user, "alice")) }()

	if err := <-shortDone; !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("short caller: err = %v, want its own deadline", err)
	}
	// The long caller now runs its own attempt, on its own budget.
	tr.waitLogins(t, 1)
	close(tr.release)
	if err := <-longDone; err != nil {
		t.Errorf("long caller: err = %v, want nil — it must not fail on "+
			"another caller's deadline", err)
	}
}

// TestEnsureSession_WaitingCallerHonoursItsOwnContext: a caller queued behind
// a slow attempt must still give up on its own deadline.
func TestEnsureSession_WaitingCallerHonoursItsOwnContext(t *testing.T) {
	tr := newSessionTracker()
	defer close(tr.release)
	user := uuid.New()

	go func() { _ = EnsureSession(context.Background(), tr, sessionCred(user, "alice")) }()
	tr.waitLogins(t, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	select {
	case err := <-func() chan error {
		c := make(chan error, 1)
		go func() { c <- EnsureSession(ctx, tr, sessionCred(user, "alice")) }()
		return c
	}():
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("err = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a waiting caller must return when its own context ends")
	}
}

// TestEnsureSession_DifferentUsersDoNotShare: the session is per user, so one
// user's login must never stand in for — or wait on — another's.
func TestEnsureSession_DifferentUsersDoNotShare(t *testing.T) {
	tr := newSessionTracker()
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { errs <- EnsureSession(context.Background(), tr, sessionCred(uuid.New(), "alice")) }()
	}
	// Both must reach Login while the first is still blocked in it.
	tr.waitLogins(t, 2)
	close(tr.release)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Errorf("caller %d: %v", i, err)
		}
	}
}

// TestEnsureSession_FailedLogin_IsRetriedNextTime: a failed login establishes
// nothing, so the next caller must log in rather than trust a stale session.
func TestEnsureSession_FailedLogin_IsRetriedNextTime(t *testing.T) {
	g := &gatedTracker{verifyOK: true, loginErr: registry.ErrSessionExpired}
	cred := sessionCred(uuid.New(), "alice")
	if err := EnsureSession(context.Background(), g, cred); !errors.Is(err, registry.ErrSessionExpired) {
		t.Fatalf("err = %v, want ErrSessionExpired", err)
	}
	g.loginErr = nil
	if err := EnsureSession(context.Background(), g, cred); err != nil {
		t.Fatal(err)
	}
	if g.loginCalls != 2 {
		t.Errorf("login calls = %d, want 2", g.loginCalls)
	}
}

// TestEnsureSession_NoCredentialsCapability_IsNoWork: a tracker without a
// login has no session to establish.
func TestEnsureSession_NoCredentialsCapability_IsNoWork(t *testing.T) {
	if err := EnsureSession(context.Background(), plainTracker{}, sessionCred(uuid.New(), "alice")); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

var _ registry.WithCredentials = (*sessionTracker)(nil)
