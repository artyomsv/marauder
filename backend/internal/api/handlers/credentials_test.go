package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// sessionRecordingTracker implements Tracker + WithCredentials and records the
// SessionEnc its Login received, so the Test-handler test can assert the stored
// session was decrypted and passed through (not just the password).
type sessionRecordingTracker struct{ gotSession string }

func (f *sessionRecordingTracker) Name() string         { return "fakesession" }
func (f *sessionRecordingTracker) DisplayName() string  { return "Fake Session" }
func (f *sessionRecordingTracker) CanParse(string) bool { return false }
func (f *sessionRecordingTracker) Parse(context.Context, string) (*domain.Topic, error) {
	return nil, nil
}
func (f *sessionRecordingTracker) Check(context.Context, *domain.Topic, *domain.TrackerCredential) (*domain.Check, error) {
	return nil, nil
}
func (f *sessionRecordingTracker) Download(context.Context, *domain.Topic, *domain.Check, *domain.TrackerCredential) (*domain.Payload, error) {
	return nil, nil
}
func (f *sessionRecordingTracker) Login(_ context.Context, creds *domain.TrackerCredential) error {
	f.gotSession = string(creds.SessionEnc)
	return nil
}
func (f *sessionRecordingTracker) Verify(context.Context, *domain.TrackerCredential) (bool, error) {
	return true, nil
}

var sessionRecorder = &sessionRecordingTracker{}

// unverifiableTracker stands in for toloka/unionpeer: Login succeeds, but the
// plugin has no way to confirm the session and says so.
type unverifiableTracker struct{ sessionRecordingTracker }

func (f *unverifiableTracker) Name() string        { return "fakeunverifiable" }
func (f *unverifiableTracker) DisplayName() string { return "Fake Unverifiable" }
func (f *unverifiableTracker) Verify(context.Context, *domain.TrackerCredential) (bool, error) {
	return false, registry.ErrVerifyUnsupported
}

func init() {
	registry.RegisterTracker(sessionRecorder)
	registry.RegisterTracker(&unverifiableTracker{})
}

// fakeCredPlugin is a programmable registry.WithCredentials implementation
// used to drive loginAndVerify through every failure mode without any
// network or DB. Only Login and Verify are exercised by the helper, so
// the Tracker-inherited methods below are minimal stubs.
type fakeCredPlugin struct {
	loginErr  error
	verifyOK  bool
	verifyErr error
}

func (f *fakeCredPlugin) Name() string         { return "fake" }
func (f *fakeCredPlugin) DisplayName() string  { return "Fake" }
func (f *fakeCredPlugin) CanParse(string) bool { return false }
func (f *fakeCredPlugin) Parse(context.Context, string) (*domain.Topic, error) {
	return nil, errors.New("not used in these tests")
}
func (f *fakeCredPlugin) Check(context.Context, *domain.Topic, *domain.TrackerCredential) (*domain.Check, error) {
	return nil, errors.New("not used in these tests")
}
func (f *fakeCredPlugin) Download(context.Context, *domain.Topic, *domain.Check, *domain.TrackerCredential) (*domain.Payload, error) {
	return nil, errors.New("not used in these tests")
}
func (f *fakeCredPlugin) Login(context.Context, *domain.TrackerCredential) error {
	return f.loginErr
}
func (f *fakeCredPlugin) Verify(context.Context, *domain.TrackerCredential) (bool, error) {
	return f.verifyOK, f.verifyErr
}

// TestLoginAndVerify is the regression test for the credentials handler
// bug where Verify returning (false, nil) was silently treated as
// success. A user reported "I entered a wrong username and the test
// login button said success" — this test pins that path closed.
func TestLoginAndVerify(t *testing.T) {
	tests := []struct {
		name         string
		plugin       *fakeCredPlugin
		wantErr      bool
		wantInErr    string // substring that must appear in the error message
		wantVerified bool   // only checked when wantErr is false
	}{
		{
			name:         "happy path: Login ok + Verify (true, nil)",
			plugin:       &fakeCredPlugin{loginErr: nil, verifyOK: true, verifyErr: nil},
			wantErr:      false,
			wantVerified: true,
		},
		{
			// A plugin that cannot check session state must NOT be able to
			// claim it did. The credential is still usable (Login succeeded),
			// so this is not an error — but it reports verified=false so the
			// caller can say "saved, could not be verified" instead of green.
			name:         "verify unsupported: usable but unverified",
			plugin:       &fakeCredPlugin{loginErr: nil, verifyErr: registry.ErrVerifyUnsupported},
			wantErr:      false,
			wantVerified: false,
		},
		{
			// Login failing still fails, even when verification is unsupported —
			// the sentinel must not become a way to swallow a real rejection.
			name:      "verify unsupported but login failed",
			plugin:    &fakeCredPlugin{loginErr: errors.New("wrong password"), verifyErr: registry.ErrVerifyUnsupported},
			wantErr:   true,
			wantInErr: "login: wrong password",
		},
		{
			name:      "login returns error",
			plugin:    &fakeCredPlugin{loginErr: errors.New("wrong password")},
			wantErr:   true,
			wantInErr: "login: wrong password",
		},
		{
			name:      "verify returns error",
			plugin:    &fakeCredPlugin{loginErr: nil, verifyErr: errors.New("502 bad gateway")},
			wantErr:   true,
			wantInErr: "verify: 502 bad gateway",
		},
		{
			// This is the bug the user reported. Before the fix the
			// handler did `if _, err := wc.Verify(...); err != nil`
			// which discarded the bool — (false, nil) was treated as
			// a successful login. The fix captures the bool and
			// treats false as failure.
			name:      "verify returns (false, nil) — the regression the user reported",
			plugin:    &fakeCredPlugin{loginErr: nil, verifyOK: false, verifyErr: nil},
			wantErr:   true,
			wantInErr: "session is not logged in",
		},
		{
			name:         "verify returns (true, nil)",
			plugin:       &fakeCredPlugin{loginErr: nil, verifyOK: true, verifyErr: nil},
			wantErr:      false,
			wantVerified: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			creds := &domain.TrackerCredential{Username: "alice", SecretEnc: []byte("pw")}
			verified, err := loginAndVerify(context.Background(), tt.plugin, creds)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("loginAndVerify: got nil, want error containing %q", tt.wantInErr)
				}
				if tt.wantInErr != "" && !strings.Contains(err.Error(), tt.wantInErr) {
					t.Errorf("loginAndVerify error = %q, want substring %q", err.Error(), tt.wantInErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("loginAndVerify: unexpected error: %v", err)
			}
			if verified != tt.wantVerified {
				t.Errorf("verified = %v, want %v", verified, tt.wantVerified)
			}
		})
	}
}

// TestCredentialsTest_DecryptsAndPassesSession pins the fix for the bug where
// the Test handler loaded only the password and never the stored session, so
// session/captcha trackers (LostFilm) always failed with "no stored session"
// even when a valid session existed. The handler must decrypt session_enc and
// hand the plaintext to Login.
func TestCredentialsTest_DecryptsAndPassesSession(t *testing.T) {
	sessionRecorder.gotSession = ""
	sessEnc, sessNonce := encPassword(t, `{"lf_session":"S"}`)
	pwEnc, pwNonce := encPassword(t, "pw")
	uid := uuid.New()
	store := &fakeCredStore{byID: &domain.TrackerCredential{
		ID:           uuid.New(),
		UserID:       uid,
		TrackerName:  "fakesession",
		Username:     "alice",
		SecretEnc:    pwEnc,
		SecretNonce:  pwNonce,
		SessionEnc:   sessEnc,
		SessionNonce: sessNonce,
	}}
	h := newCredsHandler(t, store)
	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uid, nil), "id", store.byID.ID.String())

	h.Test(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200; body %s", w.Code, w.Body.String())
	}
	if sessionRecorder.gotSession != `{"lf_session":"S"}` {
		t.Errorf("Login received SessionEnc = %q, want the decrypted session JSON",
			sessionRecorder.gotSession)
	}
}

// TestCredentialsTest_JSONBody asserts what the endpoint actually puts on the
// wire for each outcome. The unit test above covers loginAndVerify's return
// values; this covers the serialisation the UI keys on to decide between a
// green tick and an amber "unverified" notice, which is the whole point of the
// distinction.
func TestCredentialsTest_JSONBody(t *testing.T) {
	tests := []struct {
		name         string
		tracker      string
		wantVerified bool
		wantDetail   bool
	}{
		{name: "verified plugin reports verified:true", tracker: "fakesession", wantVerified: true},
		{name: "unverifiable plugin reports verified:false with a detail", tracker: "fakeunverifiable", wantVerified: false, wantDetail: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pwEnc, pwNonce := encPassword(t, "pw")
			uid := uuid.New()
			store := &fakeCredStore{byID: &domain.TrackerCredential{
				ID:          uuid.New(),
				UserID:      uid,
				TrackerName: tt.tracker,
				Username:    "alice",
				SecretEnc:   pwEnc,
				SecretNonce: pwNonce,
			}}
			h := newCredsHandler(t, store)
			w := httptest.NewRecorder()
			h.Test(w, withURLParam(authedReq(t, uid, nil), "id", store.byID.ID.String()))

			if w.Code != http.StatusOK {
				t.Fatalf("status %d, want 200; body %s", w.Code, w.Body.String())
			}
			var got struct {
				OK       bool   `json:"ok"`
				Verified bool   `json:"verified"`
				Detail   string `json:"detail"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode body %s: %v", w.Body.String(), err)
			}
			if !got.OK {
				t.Errorf("ok = false, want true (the sign-in itself did not fail)")
			}
			if got.Verified != tt.wantVerified {
				t.Errorf("verified = %v, want %v", got.Verified, tt.wantVerified)
			}
			if tt.wantDetail && got.Detail == "" {
				t.Error("an unverified result must carry a detail explaining why")
			}
			if !tt.wantDetail && got.Detail != "" {
				t.Errorf("detail = %q, want empty on a verified result", got.Detail)
			}
		})
	}
}
