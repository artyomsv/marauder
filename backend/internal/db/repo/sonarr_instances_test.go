package repo

import (
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
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

func TestSonarrInstances_ReplaceDefaultsRoundTripAndPlaceholders(t *testing.T) {
	on, off := true, false
	for _, replace := range []*bool{nil, &on, &off} {
		for _, deleteData := range []*bool{nil, &on, &off} {
			for _, key := range []string{"", "TEST-key"} {
				id := uuid.New()
				row := fullRow(id, "TEST-instance", false, "", nil, nil)
				row[11], row[12] = replace, deleteData
				fp := &fakeSonarrPool{row: row}
				r := &SonarrInstances{pool: fp}
				master := testMasterKey(t)
				input := domain.SonarrInstance{Name: "TEST-instance", APIKey: key,
					DefaultReplaceOnUpdate: replace, DefaultReplaceDeleteData: deleteData}
				got, err := r.Create(context.Background(), master, input)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got.DefaultReplaceOnUpdate, replace) || !reflect.DeepEqual(got.DefaultReplaceDeleteData, deleteData) {
					t.Fatalf("create defaults did not round-trip: %+v", got)
				}
				if fp.queryRowArgs[10] != replace || fp.queryRowArgs[11] != deleteData {
					t.Errorf("create defaults args = %v", fp.queryRowArgs)
				}
				if !strings.Contains(fp.queryRowSQL, "update_existing, owner_user_id, default_replace_on_update, default_replace_delete_data") {
					t.Errorf("create columns = %s", fp.queryRowSQL)
				}
				if key != "" && !strings.Contains(fp.queryRowSQL, "$11, $12, $13, $14)") {
					t.Errorf("create key placeholders = %s", fp.queryRowSQL)
				}
				got, err = r.Update(context.Background(), master, id, input)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got.DefaultReplaceOnUpdate, replace) || !reflect.DeepEqual(got.DefaultReplaceDeleteData, deleteData) {
					t.Fatalf("update defaults did not round-trip: %+v", got)
				}
				if fp.queryRowArgs[9] != replace || fp.queryRowArgs[10] != deleteData {
					t.Errorf("update defaults args = %v", fp.queryRowArgs)
				}
				for _, clause := range []string{"default_replace_on_update = $10", "default_replace_delete_data = $11", "RETURNING " + sonarrCols} {
					if !strings.Contains(fp.queryRowSQL, clause) {
						t.Errorf("update missing %q: %s", clause, fp.queryRowSQL)
					}
				}
				if key == "" {
					if len(fp.queryRowArgs) != 12 || fp.queryRowArgs[11] != id || !strings.Contains(fp.queryRowSQL, "WHERE id = $12") {
						t.Errorf("no-key update placeholders: %s, %v", fp.queryRowSQL, fp.queryRowArgs)
					}
				} else {
					if len(fp.queryRowArgs) != 14 || fp.queryRowArgs[13] != id || !strings.Contains(fp.queryRowSQL, "api_key_enc = $12, api_key_nonce = $13 WHERE id = $14") {
						t.Fatalf("key update placeholders: %s, %v", fp.queryRowSQL, fp.queryRowArgs)
					}
					enc, encOK := fp.queryRowArgs[11].([]byte)
					nonce, nonceOK := fp.queryRowArgs[12].([]byte)
					if !encOK || !nonceOK {
						t.Fatal("missing encrypted key arguments")
					}
					plain, err := master.DecryptString(enc, nonce)
					if err != nil || plain != key {
						t.Fatalf("key round-trip = %q, %v", plain, err)
					}
				}
			}
		}
	}
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
	queryRowSQL  string
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
func (p *fakeSonarrPool) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	p.queryRowSQL = sql
	p.queryRowArgs = args
	return fakeSonarrRow{vals: p.row, scanErr: p.queryRowErr}
}

// fullRow builds the 18-column RETURNING/SELECT row used by scanInstance.
func fullRow(id uuid.UUID, name string, enabled bool, url string, enc, nonce []byte) []any {
	return []any{
		id, name, enabled, url, enc, nonce,
		900, []string{"rutracker"}, (*uuid.UUID)(nil), "", "", (*bool)(nil), (*bool)(nil), false,
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
	// enc + nonce appended as args 13 and 14.
	if len(fp.queryRowArgs) != 14 {
		t.Fatalf("want 14 queryRow args, got %d", len(fp.queryRowArgs))
	}
	enc, _ := fp.queryRowArgs[12].([]byte)
	nonce, _ := fp.queryRowArgs[13].([]byte)
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
	// 11 SET args + id (no enc/nonce) = 12.
	if len(fp.queryRowArgs) != 12 {
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
