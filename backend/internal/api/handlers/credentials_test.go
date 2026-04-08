package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// fakeCredPlugin is a programmable registry.WithCredentials implementation
// used to drive loginAndVerify through every failure mode without any
// network or DB. Only Login and Verify are exercised by the helper, so
// the Tracker-inherited methods below are minimal stubs.
type fakeCredPlugin struct {
	loginErr  error
	verifyOK  bool
	verifyErr error
}

func (f *fakeCredPlugin) Name() string        { return "fake" }
func (f *fakeCredPlugin) DisplayName() string { return "Fake" }
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
		name      string
		plugin    *fakeCredPlugin
		wantErr   bool
		wantInErr string // substring that must appear in the error message
	}{
		{
			name:    "happy path: Login ok + Verify (true, nil)",
			plugin:  &fakeCredPlugin{loginErr: nil, verifyOK: true, verifyErr: nil},
			wantErr: false,
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
			name:    "verify returns (true, nil)",
			plugin:  &fakeCredPlugin{loginErr: nil, verifyOK: true, verifyErr: nil},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			creds := &domain.TrackerCredential{Username: "alice", SecretEnc: []byte("pw")}
			err := loginAndVerify(context.Background(), tt.plugin, creds)
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
		})
	}
}
