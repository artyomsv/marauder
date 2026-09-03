package trackercreds

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// plainTracker implements registry.Tracker only — no credentials to warm.
type plainTracker struct{}

func (plainTracker) Name() string         { return "plain-test" }
func (plainTracker) DisplayName() string  { return "Plain" }
func (plainTracker) CanParse(string) bool { return true }
func (plainTracker) Parse(context.Context, string) (*domain.Topic, error) {
	return nil, nil
}
func (plainTracker) Check(context.Context, *domain.Topic, *domain.TrackerCredential) (*domain.Check, error) {
	return nil, nil
}
func (plainTracker) Download(context.Context, *domain.Topic, *domain.Check, *domain.TrackerCredential) (*domain.Payload, error) {
	return nil, nil
}

// gatedTracker adds WithCredentials and counts each call, so the
// Verify-first/Login-on-miss ordering can be asserted rather than assumed.
type gatedTracker struct {
	plainTracker
	verifyOK    bool
	verifyErr   error
	loginErr    error
	verifyCalls int
	loginCalls  int
	sawSession  []byte
}

func (g *gatedTracker) Name() string { return "gated-test" }
func (g *gatedTracker) Verify(_ context.Context, c *domain.TrackerCredential) (bool, error) {
	g.verifyCalls++
	g.sawSession = c.SessionEnc
	return g.verifyOK, g.verifyErr
}
func (g *gatedTracker) Login(_ context.Context, c *domain.TrackerCredential) error {
	g.loginCalls++
	g.sawSession = c.SessionEnc
	return g.loginErr
}

type fakeStore struct {
	cred *domain.TrackerCredential
	err  error
}

func (f fakeStore) GetForTracker(context.Context, uuid.UUID, string) (*domain.TrackerCredential, error) {
	return f.cred, f.err
}

func masterKey(t *testing.T) *crypto.MasterKey {
	t.Helper()
	mk, err := crypto.LoadMasterKey(base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	if err != nil {
		t.Fatalf("load master key: %v", err)
	}
	return mk
}

// storedCred builds a credential the way the repo holds one: encrypted at
// rest, with an optional session blob.
func storedCred(t *testing.T, mk *crypto.MasterKey, session string) *domain.TrackerCredential {
	t.Helper()
	enc, nonce, err := mk.Encrypt([]byte("hunter2"))
	if err != nil {
		t.Fatalf("encrypt secret: %v", err)
	}
	c := &domain.TrackerCredential{
		ID: uuid.New(), UserID: uuid.New(), TrackerName: "gated-test",
		Username: "user", SecretEnc: enc, SecretNonce: nonce,
	}
	if session != "" {
		senc, snonce, err := mk.Encrypt([]byte(session))
		if err != nil {
			t.Fatalf("encrypt session: %v", err)
		}
		c.SessionEnc, c.SessionNonce = senc, snonce
	}
	return c
}

func TestWarm_TrackerWithoutCredentials_IsNoWork(t *testing.T) {
	mk := masterKey(t)
	// A store that would panic if consulted: a tracker with no login must not
	// cause a credential lookup at all.
	creds, failed, err := Warm(context.Background(), nil, mk, uuid.New(), plainTracker{})
	if creds != nil || failed || err != nil {
		t.Errorf("Warm = (%v, %v, %v), want all zero", creds, failed, err)
	}
}

// TestWarm_NoStoredCredential_IsNotALoginFailure keeps the two states apart:
// reporting "your login failed" to someone who never added an account sends
// them to fix something that is not broken.
func TestWarm_NoStoredCredential_IsNotALoginFailure(t *testing.T) {
	mk := masterKey(t)
	g := &gatedTracker{}
	creds, failed, err := Warm(context.Background(), fakeStore{}, mk, uuid.New(), g)
	if creds != nil {
		t.Error("no stored credential must yield nil")
	}
	if failed {
		t.Error("loginFailed must be false — nothing was ever tried")
	}
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if g.verifyCalls != 0 || g.loginCalls != 0 {
		t.Errorf("tracker was contacted: verify=%d login=%d", g.verifyCalls, g.loginCalls)
	}
}

// TestWarm_VerifyFirst_SkipsTheLogin is the ordering the doc promises. On a
// warm in-process session Verify is one cheap GET; paying a Login round-trip
// per call would multiply requests against trackers that rate-limit (Toloka
// 429s at six requests in three seconds).
func TestWarm_VerifyFirst_SkipsTheLogin(t *testing.T) {
	mk := masterKey(t)
	g := &gatedTracker{verifyOK: true}
	cred := storedCred(t, mk, "session-blob")

	got, failed, err := Warm(context.Background(), fakeStore{cred: cred}, mk, cred.UserID, g)
	if err != nil || failed || got == nil {
		t.Fatalf("Warm = (%v, %v, %v), want a usable credential", got, failed, err)
	}
	if g.verifyCalls != 1 {
		t.Errorf("verify calls = %d, want 1", g.verifyCalls)
	}
	if g.loginCalls != 0 {
		t.Errorf("login calls = %d, want 0 — a live session must not pay a login", g.loginCalls)
	}
	// The DECRYPTED blobs are what the plugin needs; the stored ciphertext
	// would rehydrate into a jar full of nonsense.
	if string(got.SecretEnc) != "hunter2" {
		t.Errorf("secret = %q, want it decrypted", got.SecretEnc)
	}
	if string(g.sawSession) != "session-blob" {
		t.Errorf("session = %q, want it decrypted", g.sawSession)
	}
}

// TestWarm_VerifyFalse_FallsBackToLogin: a cold or dead session is exactly
// when the login round-trip is worth paying.
func TestWarm_VerifyFalse_FallsBackToLogin(t *testing.T) {
	mk := masterKey(t)
	g := &gatedTracker{verifyOK: false}
	cred := storedCred(t, mk, "")

	got, failed, err := Warm(context.Background(), fakeStore{cred: cred}, mk, cred.UserID, g)
	if err != nil || failed || got == nil {
		t.Fatalf("Warm = (%v, %v, %v), want a usable credential", got, failed, err)
	}
	if g.loginCalls != 1 {
		t.Errorf("login calls = %d, want 1", g.loginCalls)
	}
}

// TestWarm_VerifyErrors_StillTriesLogin: an errored Verify says nothing about
// the password, so refusing to log in would strand a recoverable session.
func TestWarm_VerifyErrors_StillTriesLogin(t *testing.T) {
	mk := masterKey(t)
	g := &gatedTracker{verifyErr: errors.New("network blip")}
	cred := storedCred(t, mk, "")

	if _, _, err := Warm(context.Background(), fakeStore{cred: cred}, mk, cred.UserID, g); err != nil {
		t.Fatalf("Warm: %v", err)
	}
	if g.loginCalls != 1 {
		t.Errorf("login calls = %d, want 1", g.loginCalls)
	}
}

// TestWarm_LoginFails_ReportsItAndDegrades: nil creds so the caller proceeds
// anonymously, but loginFailed true so search can say "your login failed"
// rather than "add an account".
func TestWarm_LoginFails_ReportsItAndDegrades(t *testing.T) {
	mk := masterKey(t)
	boom := errors.New("invalid credentials")
	g := &gatedTracker{loginErr: boom}
	cred := storedCred(t, mk, "")

	got, failed, err := Warm(context.Background(), fakeStore{cred: cred}, mk, cred.UserID, g)
	if got != nil {
		t.Error("a failed login must not yield a credential")
	}
	if !failed {
		t.Error("loginFailed must be true — an account exists and did not work")
	}
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want the login error", err)
	}
}

// TestWarm_UndecryptableSecret_DegradesRatherThanErroringOut. Every caller is
// fail-open; a corrupt blob must not take down a search or block a topic being
// created.
func TestWarm_UndecryptableSecret_DegradesRatherThanErroringOut(t *testing.T) {
	mk := masterKey(t)
	g := &gatedTracker{verifyOK: true}
	cred := storedCred(t, mk, "")
	cred.SecretEnc = []byte("not ciphertext")

	got, failed, err := Warm(context.Background(), fakeStore{cred: cred}, mk, cred.UserID, g)
	if got != nil || !failed || err == nil {
		t.Errorf("Warm = (%v, %v, %v), want nil creds + loginFailed + an error", got, failed, err)
	}
	if g.verifyCalls != 0 {
		t.Error("the tracker must not be contacted with an undecryptable secret")
	}
}

// TestWarm_NilStoreOrMaster_IsAnonymous: the Sonarr poller and the handlers
// both accept a nil store, and nil must mean "resolve anonymously", never a
// panic on a background loop.
func TestWarm_NilStoreOrMaster_IsAnonymous(t *testing.T) {
	g := &gatedTracker{verifyOK: true}
	if got, _, _ := Warm(context.Background(), nil, masterKey(t), uuid.New(), g); got != nil {
		t.Error("a nil store must resolve anonymously")
	}
	if got, _, _ := Warm(context.Background(), fakeStore{}, nil, uuid.New(), g); got != nil {
		t.Error("a nil decryptor must resolve anonymously")
	}
}

// TestWarm_StoreError_IsAnonymousNotAFailure: a DB blip is not the user's
// account being wrong, so it must not be reported as a login failure.
func TestWarm_StoreError_IsAnonymousNotAFailure(t *testing.T) {
	g := &gatedTracker{}
	got, failed, err := Warm(context.Background(), fakeStore{err: errors.New("db down")},
		masterKey(t), uuid.New(), g)
	if got != nil || failed || err != nil {
		t.Errorf("Warm = (%v, %v, %v), want a clean anonymous result", got, failed, err)
	}
}

var _ registry.Tracker = plainTracker{}
var _ registry.WithCredentials = (*gatedTracker)(nil)
