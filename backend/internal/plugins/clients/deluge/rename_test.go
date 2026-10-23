package deluge

import (
	"context"
	"reflect"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

var _ registry.WithRename = (*plugin)(nil)

func lastCall(calls *[]map[string]any, method string) map[string]any {
	for i := len(*calls) - 1; i >= 0; i-- {
		if (*calls)[i]["method"] == method {
			return (*calls)[i]
		}
	}
	return nil
}

// core.rename_folder wants folder paths with a trailing slash.
func TestRenameRoot_AddsTrailingSlashOnce(t *testing.T) {
	for _, tc := range []struct{ from, to string }{{"Old", "New"}, {"Old/", "New/"}} {
		srv, calls := newSelectServer(t, `{}`)
		p := &plugin{sessions: map[string]*session{}}
		if err := p.RenameRoot(context.Background(), delugeCfg(srv.URL), "abc", tc.from, tc.to); err != nil {
			t.Fatalf("RenameRoot(%q, %q): %v", tc.from, tc.to, err)
		}
		got := lastCall(calls, "core.rename_folder")
		if got == nil {
			t.Fatalf("core.rename_folder not called")
		}
		want := []any{"abc", "Old/", "New/"}
		if !reflect.DeepEqual(got["params"], want) {
			t.Errorf("params = %v, want %v", got["params"], want)
		}
	}
}

// core.force_recheck takes a list of torrent ids.
func TestRecheck_SendsAList(t *testing.T) {
	srv, calls := newSelectServer(t, `{}`)
	p := &plugin{sessions: map[string]*session{}}
	if err := p.Recheck(context.Background(), delugeCfg(srv.URL), "abc"); err != nil {
		t.Fatalf("Recheck: %v", err)
	}
	got := lastCall(calls, "core.force_recheck")
	if got == nil || !reflect.DeepEqual(got["params"], []any{[]any{"abc"}}) {
		t.Errorf("call = %v, want core.force_recheck with [[abc]]", got)
	}
}

func TestBytesDone_ReadsTotalDone(t *testing.T) {
	srv, calls := newSelectServer(t, `{"total_done":2048}`)
	p := &plugin{sessions: map[string]*session{}}
	got, err := p.BytesDone(context.Background(), delugeCfg(srv.URL), "abc")
	if err != nil || got != 2048 {
		t.Errorf("BytesDone = (%d, %v), want (2048, nil)", got, err)
	}
	call := lastCall(calls, "core.get_torrent_status")
	if call == nil || !reflect.DeepEqual(call["params"], []any{"abc", []any{"total_done"}}) {
		t.Errorf("call = %v, want get_torrent_status for total_done", call)
	}
}

// Deluge answers an unknown torrent id with an empty status dictionary.
func TestBytesDone_UnknownTorrentIsAnError(t *testing.T) {
	srv, _ := newSelectServer(t, `{}`)
	p := &plugin{sessions: map[string]*session{}}
	if _, err := p.BytesDone(context.Background(), delugeCfg(srv.URL), "abc"); err == nil {
		t.Error("BytesDone for an unknown torrent = nil error, want one")
	}
}
