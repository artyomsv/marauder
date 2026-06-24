package repo

import (
	"context"
	"encoding/base64"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/domain"
)

// fakeSettingsRow assigns prepared column values into Scan destinations.
type fakeSettingsRow struct{ vals []any }

func (r fakeSettingsRow) Scan(dest ...any) error {
	for i := range dest {
		reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(r.vals[i]))
	}
	return nil
}

type fakeSettingsPool struct {
	execArgs []any
	row      []any
}

func (p *fakeSettingsPool) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	p.execArgs = args
	return pgconn.CommandTag{}, nil
}
func (p *fakeSettingsPool) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return fakeSettingsRow{vals: p.row}
}

func testMasterKey(t *testing.T) *crypto.MasterKey {
	t.Helper()
	mk, err := crypto.LoadMasterKey(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatalf("load master key: %v", err)
	}
	return mk
}

func TestSettings_SonarrEncryptRoundTrip(t *testing.T) {
	master := testMasterKey(t)
	fp := &fakeSettingsPool{}
	r := &Settings{pool: fp}

	cfg := &domain.SonarrConfig{
		Enabled: true, URL: "http://sonarr:8989", APIKey: "super-secret",
		PollIntervalSec: 900, AllowedTrackers: []string{"rutracker"},
	}
	if err := r.UpsertSonarr(context.Background(), master, cfg); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// With a key supplied, enc + nonce are appended as args 10 and 11.
	if len(fp.execArgs) != 11 {
		t.Fatalf("want 11 exec args, got %d", len(fp.execArgs))
	}
	enc, _ := fp.execArgs[9].([]byte)
	nonce, _ := fp.execArgs[10].([]byte)
	if len(enc) == 0 || string(enc) == "super-secret" {
		t.Fatalf("api key not encrypted at rest")
	}
	if dec, _ := master.DecryptString(enc, nonce); dec != "super-secret" {
		t.Errorf("decrypt mismatch: %q", dec)
	}

	// GetSonarr must decrypt those same bytes back to plaintext.
	fp.row = []any{
		true, "http://sonarr:8989", enc, nonce, 900, []string{"rutracker"},
		(*uuid.UUID)(nil), "", "", false, (*uuid.UUID)(nil), (*time.Time)(nil),
	}
	got, err := r.GetSonarr(context.Background(), master)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.APIKey != "super-secret" {
		t.Errorf("round-trip key = %q", got.APIKey)
	}
	if !got.Enabled || got.URL != "http://sonarr:8989" {
		t.Errorf("scalar round-trip wrong: %+v", got)
	}
}

func TestSettings_EmptyKeyPreservesExisting(t *testing.T) {
	fp := &fakeSettingsPool{}
	r := &Settings{pool: fp}

	cfg := &domain.SonarrConfig{Enabled: false, APIKey: ""} // empty = keep stored key
	if err := r.UpsertSonarr(context.Background(), testMasterKey(t), cfg); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if len(fp.execArgs) != 9 {
		t.Errorf("empty key must not write enc/nonce columns; got %d args", len(fp.execArgs))
	}
}
