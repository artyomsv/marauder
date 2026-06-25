package repo

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// fakeRow implements pgx.Row for QueryRow-based Record.
type fakeRow struct{ id int64 }

func (r fakeRow) Scan(dest ...any) error {
	*(dest[0].(*int64)) = r.id
	return nil
}

type fakeTEPool struct {
	lastSQL  string
	lastArgs []any
	row      fakeRow
}

func (f *fakeTEPool) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	f.lastSQL = sql
	f.lastArgs = args
	return f.row
}
func (f *fakeTEPool) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return nil, nil
}
func (f *fakeTEPool) Exec(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func TestTopicEvents_Record_ReturnsID_AndMarshalsData(t *testing.T) {
	pool := &fakeTEPool{row: fakeRow{id: 7}}
	r := &TopicEvents{pool: pool}
	tid, uid := uuid.New(), uuid.New()
	id, err := r.Record(context.Background(), &domain.TopicEvent{
		TopicID: tid, UserID: uid, EventType: "release.found", Severity: "info",
		Message: "New release", Data: map[string]any{"labels": []string{"s01e01"}},
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if id != 7 {
		t.Errorf("id = %d, want 7", id)
	}
	// data arg must be JSON bytes, not a raw map (pgx can't encode map->jsonb directly here)
	raw, ok := pool.lastArgs[5].([]byte)
	if !ok {
		t.Fatalf("data arg type %T, want []byte", pool.lastArgs[5])
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("data not valid JSON: %v", err)
	}
}
