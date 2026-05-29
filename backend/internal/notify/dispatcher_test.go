package notify

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// ---------------------------------------------------------------------------
// Fake notifier plugins
// ---------------------------------------------------------------------------

// fakeNotifier is a configurable test plugin for the notifier registry.
type fakeNotifier struct {
	name    string
	sendErr error
}

func (f *fakeNotifier) Name() string                           { return f.name }
func (f *fakeNotifier) DisplayName() string                    { return f.name }
func (f *fakeNotifier) ConfigSchema() map[string]any           { return nil }
func (f *fakeNotifier) Test(_ context.Context, _ []byte) error { return nil }
func (f *fakeNotifier) Send(_ context.Context, _ []byte, _ domain.Message) error {
	return f.sendErr
}

// okPlugin always succeeds; failPlugin always returns an error.
// Registered once in init() — RegisterNotifier panics on duplicate names.
var (
	okPlugin   = &fakeNotifier{name: "test-ok-notifier"}
	failPlugin = &fakeNotifier{name: "test-fail-notifier", sendErr: errors.New("send failure")}
)

func init() {
	registry.RegisterNotifier(okPlugin)
	registry.RegisterNotifier(failPlugin)
}

// ---------------------------------------------------------------------------
// Fake repo
// ---------------------------------------------------------------------------

type fakeRepo struct {
	items []*domain.Notifier
	err   error
}

func (f *fakeRepo) ListForUser(_ context.Context, _ uuid.UUID) ([]*domain.Notifier, error) {
	return f.items, f.err
}

// ---------------------------------------------------------------------------
// Helper: build a Dispatcher + pre-encrypted config for tests
// ---------------------------------------------------------------------------

func newTestDispatcher(t *testing.T, repo notifiersRepo) (*Dispatcher, []byte, []byte) {
	t.Helper()
	mk, err := crypto.LoadMasterKey(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatalf("LoadMasterKey: %v", err)
	}
	enc, nonce, err := mk.Encrypt([]byte("{}"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	d := New(repo, mk, zerolog.Nop())
	return d, enc, nonce
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestSend_AllSucceed_ReturnsCorrectCount(t *testing.T) {
	uid := uuid.New()
	repo := &fakeRepo{}
	d, enc, nonce := newTestDispatcher(t, repo)

	repo.items = []*domain.Notifier{
		{ID: uuid.New(), UserID: uid, NotifierName: okPlugin.Name(), ConfigEnc: enc, ConfigNonce: nonce},
		{ID: uuid.New(), UserID: uid, NotifierName: okPlugin.Name(), ConfigEnc: enc, ConfigNonce: nonce},
	}

	got := d.Send(context.Background(), uid, domain.Message{Title: "t", Body: "b"})
	if got != 2 {
		t.Errorf("want 2 successes, got %d", got)
	}
}

func TestSend_OneFailOneOk_ReturnsOne(t *testing.T) {
	uid := uuid.New()
	repo := &fakeRepo{}
	d, enc, nonce := newTestDispatcher(t, repo)

	repo.items = []*domain.Notifier{
		{ID: uuid.New(), UserID: uid, NotifierName: okPlugin.Name(), ConfigEnc: enc, ConfigNonce: nonce},
		{ID: uuid.New(), UserID: uid, NotifierName: failPlugin.Name(), ConfigEnc: enc, ConfigNonce: nonce},
	}

	got := d.Send(context.Background(), uid, domain.Message{Title: "t", Body: "b"})
	if got != 1 {
		t.Errorf("want 1 success, got %d", got)
	}
}

func TestSend_EmptyList_ReturnsZero(t *testing.T) {
	uid := uuid.New()
	repo := &fakeRepo{items: []*domain.Notifier{}}
	d, _, _ := newTestDispatcher(t, repo)

	got := d.Send(context.Background(), uid, domain.Message{Title: "t"})
	if got != 0 {
		t.Errorf("want 0, got %d", got)
	}
}

func TestSend_UnknownPlugin_Skipped(t *testing.T) {
	uid := uuid.New()
	repo := &fakeRepo{}
	d, enc, nonce := newTestDispatcher(t, repo)

	repo.items = []*domain.Notifier{
		{ID: uuid.New(), UserID: uid, NotifierName: "no-such-plugin", ConfigEnc: enc, ConfigNonce: nonce},
		{ID: uuid.New(), UserID: uid, NotifierName: okPlugin.Name(), ConfigEnc: enc, ConfigNonce: nonce},
	}

	got := d.Send(context.Background(), uid, domain.Message{Title: "t"})
	// unknown plugin is skipped; the ok one still runs
	if got != 1 {
		t.Errorf("want 1, got %d", got)
	}
}

func TestSend_ListError_ReturnsZero(t *testing.T) {
	uid := uuid.New()
	repo := &fakeRepo{err: errors.New("db error")}
	d, _, _ := newTestDispatcher(t, repo)

	got := d.Send(context.Background(), uid, domain.Message{Title: "t"})
	if got != 0 {
		t.Errorf("want 0 on list error, got %d", got)
	}
}

func TestSend_DecryptError_Skipped(t *testing.T) {
	uid := uuid.New()
	repo := &fakeRepo{}
	d, enc, nonce := newTestDispatcher(t, repo)

	repo.items = []*domain.Notifier{
		{ID: uuid.New(), UserID: uid, NotifierName: okPlugin.Name(), ConfigEnc: []byte("not-encrypted"), ConfigNonce: []byte("bad")},
		{ID: uuid.New(), UserID: uid, NotifierName: okPlugin.Name(), ConfigEnc: enc, ConfigNonce: nonce},
	}

	got := d.Send(context.Background(), uid, domain.Message{Title: "t"})
	// decrypt failure is skipped; the sibling ok notifier still fires
	if got != 1 {
		t.Errorf("want 1, got %d", got)
	}
}
