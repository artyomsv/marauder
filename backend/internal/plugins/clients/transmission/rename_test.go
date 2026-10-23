package transmission

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

var _ registry.WithRename = (*plugin)(nil)

func TestRenameRoot_SendsRenamePath(t *testing.T) {
	srv, calls := newSelectServer(t, `[]`)
	if err := newPlugin().RenameRoot(context.Background(), []byte(`{"url":"`+srv.URL+`"}`), "abc", "Old", "New"); err != nil {
		t.Fatalf("RenameRoot: %v", err)
	}
	got := (*calls)[len(*calls)-1]
	if got["method"] != "torrent-rename-path" {
		t.Fatalf("method = %v, want torrent-rename-path", got["method"])
	}
	want := map[string]any{"ids": []any{"abc"}, "path": "Old", "name": "New"}
	if !reflect.DeepEqual(got["arguments"], want) {
		t.Errorf("arguments = %v, want %v", got["arguments"], want)
	}
}

func TestRenameRoot_RejectedIsAnError(t *testing.T) {
	const sessionID = "sess-1"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") != sessionID {
			w.Header().Set("X-Transmission-Session-Id", sessionID)
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.Write([]byte(`{"result":"Invalid argument","arguments":{}}`))
	}))
	defer srv.Close()

	if err := newPlugin().RenameRoot(context.Background(), []byte(`{"url":"`+srv.URL+`"}`), "abc", "Old", "New"); err == nil {
		t.Error("RenameRoot with a rejected result = nil error, want one")
	}
}

func TestRecheck_SendsTorrentVerify(t *testing.T) {
	srv, calls := newSelectServer(t, `[]`)
	if err := newPlugin().Recheck(context.Background(), []byte(`{"url":"`+srv.URL+`"}`), "abc"); err != nil {
		t.Fatalf("Recheck: %v", err)
	}
	got := (*calls)[len(*calls)-1]
	if got["method"] != "torrent-verify" || !reflect.DeepEqual(got["arguments"], map[string]any{"ids": []any{"abc"}}) {
		t.Errorf("call = %v, want torrent-verify for abc", got)
	}
}

// Bytes on disk are what can be overwritten, so BytesDone counts verified
// and unverified data, not what was ever downloaded.
func TestBytesDone_SumsHaveValidAndUnchecked(t *testing.T) {
	srv, calls := newSelectServer(t, `[{"hashString":"abc","haveValid":1000,"haveUnchecked":24}]`)
	got, err := newPlugin().BytesDone(context.Background(), []byte(`{"url":"`+srv.URL+`"}`), "abc")
	if err != nil || got != 1024 {
		t.Errorf("BytesDone = (%d, %v), want (1024, nil)", got, err)
	}
	args, _ := (*calls)[len(*calls)-1]["arguments"].(map[string]any)
	raw, _ := json.Marshal(args["fields"])
	if string(raw) != `["haveValid","haveUnchecked"]` {
		t.Errorf("fields = %s, want haveValid and haveUnchecked", raw)
	}
}

func TestBytesDone_UnknownTorrentIsAnError(t *testing.T) {
	srv, _ := newSelectServer(t, `[]`)
	if _, err := newPlugin().BytesDone(context.Background(), []byte(`{"url":"`+srv.URL+`"}`), "abc"); err == nil {
		t.Error("BytesDone for an unknown torrent = nil error, want one")
	}
}
