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
// user, the way every forum plugin's SessionStore does: Login establishes it
// and Verify reports it. Verify snapshots the state on entry and then blocks
// on release, so callers that overlap all see the same cold session — which is
// exactly the burst issue #198 measured as four 503s inside 20 ms.
type sessionTracker struct {
	plainTracker
	release chan struct{}

	mu       sync.Mutex
	loggedIn map[uuid.UUID]bool

	verifyCalls atomic.Int32
	loginCalls  atomic.Int32
	verifying   chan struct{}
}

func newSessionTracker() *sessionTracker {
	return &sessionTracker{
		release:   make(chan struct{}),
		loggedIn:  map[uuid.UUID]bool{},
		verifying: make(chan struct{}, 64),
	}
}

func (s *sessionTracker) Name() string { return "session-test" }

func (s *sessionTracker) Verify(ctx context.Context, c *domain.TrackerCredential) (bool, error) {
	s.verifyCalls.Add(1)
	s.mu.Lock()
	ok := s.loggedIn[c.UserID]
	s.mu.Unlock()
	s.verifying <- struct{}{}
	<-s.release
	// A real request fails on a cancelled context; so does this one.
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return ok, nil
}

func (s *sessionTracker) Login(ctx context.Context, c *domain.TrackerCredential) error {
	s.loginCalls.Add(1)
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.loggedIn[c.UserID] = true
	s.mu.Unlock()
	return nil
}

func (s *sessionTracker) waitVerifying(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-s.verifying:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of %d Verify calls started", i, n)
		}
	}
}

// joinGrace is how long a test waits for goroutines that are already running
// to reach EnsureSession. Too short only lets a straggler run its own attempt
// after the shared one finished — which then finds a live session, so it can
// weaken the concurrency tests but never make them fail wrongly.
const joinGrace = 100 * time.Millisecond

func sessionCred(user uuid.UUID) *domain.TrackerCredential {
	return &domain.TrackerCredential{
		ID: uuid.New(), UserID: user, TrackerName: "session-test",
		Username: "user", SecretEnc: []byte("hunter2"),
	}
}

// TestEnsureSession_ConcurrentCallersShareOneLogin is issue #198: the
// scheduler's workers check one user's topics at the same moment, and each
// sent its own login. Tapochek answered four of them with 503.
func TestEnsureSession_ConcurrentCallersShareOneLogin(t *testing.T) {
	tr := newSessionTracker()
	user := uuid.New()
	const callers = 8

	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		go func() { errs <- EnsureSession(context.Background(), tr, sessionCred(user)) }()
	}
	tr.waitVerifying(t, 1)
	time.Sleep(joinGrace)
	close(tr.release)

	for i := 0; i < callers; i++ {
		if err := <-errs; err != nil {
			t.Errorf("caller %d: %v", i, err)
		}
	}
	if got := tr.loginCalls.Load(); got != 1 {
		t.Errorf("login calls = %d, want 1 — concurrent checks of one user's "+
			"topics must share a single login", got)
	}
}

// TestEnsureSession_LiveSession_SkipsLogin: once a session exists, a check
// costs one Verify and no login at all.
func TestEnsureSession_LiveSession_SkipsLogin(t *testing.T) {
	tr := newSessionTracker()
	close(tr.release)
	user := uuid.New()

	for i := 0; i < 3; i++ {
		if err := EnsureSession(context.Background(), tr, sessionCred(user)); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if got := tr.loginCalls.Load(); got != 1 {
		t.Errorf("login calls = %d, want 1 (the cold start only)", got)
	}
}

// TestEnsureSession_DifferentUsersDoNotShare: the session is per user, so one
// user's login must never stand in for another's.
func TestEnsureSession_DifferentUsersDoNotShare(t *testing.T) {
	tr := newSessionTracker()
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { errs <- EnsureSession(context.Background(), tr, sessionCred(uuid.New())) }()
	}
	// Both must reach Verify while the first is still blocked in it.
	tr.waitVerifying(t, 2)
	close(tr.release)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Errorf("caller %d: %v", i, err)
		}
	}
	if got := tr.loginCalls.Load(); got != 2 {
		t.Errorf("login calls = %d, want 2 — one per user", got)
	}
}

// TestEnsureSession_CancelledCallerDoesNotFailTheOthers: a shared attempt runs
// on whichever caller arrived first. If that caller goes away — a closed
// preview request, a scheduler worker on shutdown — the callers still waiting
// must not inherit its cancellation as a login failure, which the scheduler
// would record against every one of their topics.
func TestEnsureSession_CancelledCallerDoesNotFailTheOthers(t *testing.T) {
	tr := newSessionTracker()
	user := uuid.New()

	leadCtx, cancelLead := context.WithCancel(context.Background())
	leadErr := make(chan error, 1)
	go func() { leadErr <- EnsureSession(leadCtx, tr, sessionCred(user)) }()
	tr.waitVerifying(t, 1)

	followErr := make(chan error, 1)
	go func() { followErr <- EnsureSession(context.Background(), tr, sessionCred(user)) }()
	time.Sleep(joinGrace)

	cancelLead()
	select {
	case err := <-leadErr:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("cancelled caller: err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a cancelled caller must return without waiting for the attempt")
	}

	close(tr.release)
	if err := <-followErr; err != nil {
		t.Errorf("waiting caller: err = %v, want nil — it must not inherit "+
			"another caller's cancellation", err)
	}
}

// TestEnsureSession_LoginErrorReachesEveryCaller: a real failure is shared,
// not swallowed — the scheduler still needs ErrSessionExpired to raise the
// re-authentication notice.
func TestEnsureSession_LoginErrorReachesEveryCaller(t *testing.T) {
	g := &gatedTracker{loginErr: registry.ErrSessionExpired}
	err := EnsureSession(context.Background(), g, sessionCred(uuid.New()))
	if !errors.Is(err, registry.ErrSessionExpired) {
		t.Errorf("err = %v, want ErrSessionExpired", err)
	}
}

// TestEnsureSession_NoCredentialsCapability_IsNoWork: a tracker without a
// login has no session to establish.
func TestEnsureSession_NoCredentialsCapability_IsNoWork(t *testing.T) {
	if err := EnsureSession(context.Background(), plainTracker{}, sessionCred(uuid.New())); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

var _ registry.WithCredentials = (*sessionTracker)(nil)
