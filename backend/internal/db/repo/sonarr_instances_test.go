package repo

import (
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/domain"
)

func testMasterKey(t *testing.T) *crypto.MasterKey {
	t.Helper()
	mk, err := crypto.LoadMasterKey(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatalf("load master key: %v", err)
	}
	return mk
}

// fakeSonarrRow assigns prepared column values into Scan destinations, or
// returns scanErr (e.g. pgx.ErrNoRows) when set.
type fakeSonarrRow struct {
	vals    []any
	scanErr error
}

func (r fakeSonarrRow) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	for i := range dest {
		reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(r.vals[i]))
	}
	return nil
}

// fakeSonarrRows iterates a slice of value rows.
type fakeSonarrRows struct {
	pgx.Rows
	rows [][]any
	i    int
}

func (f *fakeSonarrRows) Next() bool { f.i++; return f.i <= len(f.rows) }
func (f *fakeSonarrRows) Scan(dest ...any) error {
	vals := f.rows[f.i-1]
	for i := range dest {
		reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(vals[i]))
	}
	return nil
}
func (f *fakeSonarrRows) Close()     {}
func (f *fakeSonarrRows) Err() error { return nil }

type fakeSonarrPool struct {
	queryRowArgs []any
	execArgs     []any
	row          []any
	rows         [][]any
	queryRowErr  error
}

func (p *fakeSonarrPool) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	p.execArgs = args
	return pgconn.CommandTag{}, nil
}
func (p *fakeSonarrPool) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return &fakeSonarrRows{rows: p.rows}, nil
}
func (p *fakeSonarrPool) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	p.queryRowArgs = args
	return fakeSonarrRow{vals: p.row, scanErr: p.queryRowErr}
}

// fullRow builds the 16-column RETURNING/SELECT row used by scanInstance.
func fullRow(id uuid.UUID, name string, enabled bool, url string, enc, nonce []byte) []any {
	return []any{
		id, name, enabled, url, enc, nonce,
		900, []string{"rutracker"}, (*uuid.UUID)(nil), "", "", false,
		(*uuid.UUID)(nil), (*time.Time)(nil), time.Now(), time.Now(),
	}
}

func TestSonarrInstances_Create_EncryptsKey(t *testing.T) {
	master := testMasterKey(t)
	fp := &fakeSonarrPool{row: fullRow(uuid.New(), "TV", true, "http://sonarr:8989", nil, nil)}
	r := &SonarrInstances{pool: fp}

	_, err := r.Create(context.Background(), master, domain.SonarrInstance{
		Name: "TV", Enabled: true, URL: "http://sonarr:8989", APIKey: "super-secret",
		PollIntervalSec: 900, AllowedTrackers: []string{"rutracker"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// enc + nonce appended as args 11 and 12.
	if len(fp.queryRowArgs) != 12 {
		t.Fatalf("want 12 queryRow args, got %d", len(fp.queryRowArgs))
	}
	enc, _ := fp.queryRowArgs[10].([]byte)
	nonce, _ := fp.queryRowArgs[11].([]byte)
	if len(enc) == 0 || string(enc) == "super-secret" {
		t.Fatalf("api key not encrypted at rest")
	}
	if dec, _ := master.DecryptString(enc, nonce); dec != "super-secret" {
		t.Errorf("decrypt mismatch: %q", dec)
	}
}

func TestSonarrInstances_Get_DecryptsKey(t *testing.T) {
	master := testMasterKey(t)
	enc, nonce, err := master.EncryptString("super-secret")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	id := uuid.New()
	fp := &fakeSonarrPool{row: fullRow(id, "TV", true, "http://sonarr:8989", enc, nonce)}
	r := &SonarrInstances{pool: fp}

	got, err := r.Get(context.Background(), master, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.APIKey != "super-secret" {
		t.Errorf("round-trip key = %q", got.APIKey)
	}
	if got.Name != "TV" || !got.Enabled || got.URL != "http://sonarr:8989" {
		t.Errorf("scalar round-trip wrong: %+v", got)
	}
}

func TestSonarrInstances_Update_EmptyKeyPreservesExisting(t *testing.T) {
	fp := &fakeSonarrPool{row: fullRow(uuid.New(), "TV", false, "", nil, nil)}
	r := &SonarrInstances{pool: fp}

	_, err := r.Update(context.Background(), testMasterKey(t), uuid.New(),
		domain.SonarrInstance{Name: "TV", APIKey: ""}) // empty = keep stored key
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	// 9 SET args + id (no enc/nonce) = 10.
	if len(fp.queryRowArgs) != 10 {
		t.Errorf("empty key must not write enc/nonce; got %d args", len(fp.queryRowArgs))
	}
}

func TestSonarrInstances_Get_NotFoundMapped(t *testing.T) {
	fp := &fakeSonarrPool{queryRowErr: pgx.ErrNoRows}
	r := &SonarrInstances{pool: fp}
	_, err := r.Get(context.Background(), testMasterKey(t), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("missing instance must map to ErrNotFound, got %v", err)
	}
}

func TestSonarrInstances_UpdateCursor(t *testing.T) {
	fp := &fakeSonarrPool{}
	r := &SonarrInstances{pool: fp}
	id := uuid.New()
	ts := time.Now().UTC()
	if err := r.UpdateCursor(context.Background(), id, ts); err != nil {
		t.Fatalf("cursor: %v", err)
	}
	if len(fp.execArgs) != 2 || fp.execArgs[1] != id {
		t.Errorf("cursor exec args wrong: %v", fp.execArgs)
	}
}

func TestSonarrInstances_Delete(t *testing.T) {
	fp := &fakeSonarrPool{}
	r := &SonarrInstances{pool: fp}
	id := uuid.New()
	if err := r.Delete(context.Background(), id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(fp.execArgs) != 1 || fp.execArgs[0] != id {
		t.Errorf("delete exec args wrong: %v", fp.execArgs)
	}
}

func TestSonarrInstances_List(t *testing.T) {
	master := testMasterKey(t)
	fp := &fakeSonarrPool{rows: [][]any{
		fullRow(uuid.New(), "TV", true, "http://tv:8989", nil, nil),
		fullRow(uuid.New(), "Anime", false, "http://anime:8989", nil, nil),
	}}
	r := &SonarrInstances{pool: fp}
	got, err := r.List(context.Background(), master)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("want 2 instances, got %d", len(got))
	}
}

func TestSonarrInstances_ListEnabled(t *testing.T) {
	master := testMasterKey(t)
	fp := &fakeSonarrPool{rows: [][]any{
		fullRow(uuid.New(), "TV", true, "http://tv:8989", nil, nil),
		fullRow(uuid.New(), "Anime", true, "http://anime:8989", nil, nil),
	}}
	r := &SonarrInstances{pool: fp}
	got, err := r.ListEnabled(context.Background(), master)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 || got[0].Name != "TV" || got[1].Name != "Anime" {
		t.Errorf("list result wrong: %+v", got)
	}
}
