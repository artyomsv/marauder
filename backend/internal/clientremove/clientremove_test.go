package clientremove

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// fakeClient implements registry.Client but NOT registry.WithRemoval, so it
// stands in for a client that cannot remove torrents (downloadfolder).
type fakeClient struct{ name string }

func (f *fakeClient) Name() string                       { return f.name }
func (f *fakeClient) DisplayName() string                { return f.name }
func (f *fakeClient) ConfigSchema() map[string]any       { return map[string]any{} }
func (f *fakeClient) Test(context.Context, []byte) error { return nil }
func (f *fakeClient) Add(context.Context, []byte, *domain.Payload, domain.AddOptions) error {
	return nil
}

// fakeRemovalClient additionally implements registry.WithRemoval and records
// the arguments it was called with.
type fakeRemovalClient struct {
	fakeClient
	err       error
	gotHashes []string
	gotDelete bool
	gotRawCfg []byte
	gotCtx    context.Context
	callCount int
}

func (f *fakeRemovalClient) Remove(ctx context.Context, rawConfig []byte, hashes []string, deleteData bool) error {
	f.callCount++
	f.gotCtx = ctx
	f.gotRawCfg, f.gotHashes, f.gotDelete = rawConfig, hashes, deleteData
	return f.err
}

type fakeClients struct {
	client *domain.Client
	err    error
}

func (f *fakeClients) GetByID(context.Context, uuid.UUID, uuid.UUID) (*domain.Client, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.client, nil
}

type fakeDecryptor struct {
	plain []byte
	err   error
}

func (f *fakeDecryptor) Decrypt([]byte, []byte) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.plain, nil
}

func TestRemover_Remove_Success(t *testing.T) {
	plugin := &fakeRemovalClient{fakeClient: fakeClient{name: "qbittorrent"}}
	clientID := uuid.New()
	r := &Remover{
		Clients: &fakeClients{client: &domain.Client{ClientName: "qbittorrent"}},
		Master:  &fakeDecryptor{plain: []byte(`{"url":"http://qb:6611"}`)},
		Lookup:  func(string) registry.Client { return plugin },
		Timeout: time.Second,
	}

	got := r.Remove(context.Background(), uuid.New(),
		map[uuid.UUID][]string{clientID: {"aaa", "bbb"}}, true)

	if len(got) != 1 {
		t.Fatalf("want 1 result, got %d", len(got))
	}
	if !got[0].OK || got[0].Reason != "" {
		t.Fatalf("want OK result, got %+v", got[0])
	}
	if got[0].ClientName != "qbittorrent" || got[0].ClientID != clientID {
		t.Fatalf("result identity wrong: %+v", got[0])
	}
	if !plugin.gotDelete {
		t.Fatal("deleteData was not forwarded to the plugin")
	}
	if len(plugin.gotHashes) != 2 {
		t.Fatalf("want 2 hashes forwarded, got %v", plugin.gotHashes)
	}
	if string(plugin.gotRawCfg) != `{"url":"http://qb:6611"}` {
		t.Fatalf("decrypted config not forwarded: %s", plugin.gotRawCfg)
	}
}

// TestRemover_Remove_TimeoutValues pins what a zero Timeout means. The field is
// exported and the package now has two construction sites, so a caller that
// leaves it unset must inherit the caller's deadline rather than hit an
// already-expired context — context.WithTimeout(ctx, 0) fires immediately and
// would fail every removal without ever reaching the client.
func TestRemover_Remove_TimeoutValues(t *testing.T) {
	tests := []struct {
		name         string
		timeout      time.Duration
		wantDeadline bool
	}{
		{"zero inherits the caller's context", 0, false},
		{"positive bounds the call", time.Second, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin := &fakeRemovalClient{fakeClient: fakeClient{name: "qbittorrent"}}
			r := &Remover{
				Clients: &fakeClients{client: &domain.Client{ClientName: "qbittorrent"}},
				Master:  &fakeDecryptor{plain: []byte("{}")},
				Lookup:  func(string) registry.Client { return plugin },
				Timeout: tt.timeout,
			}

			got := r.Remove(context.Background(), uuid.New(),
				map[uuid.UUID][]string{uuid.New(): {"aaa"}}, false)

			if len(got) != 1 || !got[0].OK {
				t.Fatalf("removal failed with Timeout=%v: %+v", tt.timeout, got)
			}
			if plugin.callCount != 1 {
				t.Fatalf("plugin Remove calls = %d, want 1", plugin.callCount)
			}
			if _, hasDeadline := plugin.gotCtx.Deadline(); hasDeadline != tt.wantDeadline {
				t.Errorf("ctx has deadline = %v, want %v", hasDeadline, tt.wantDeadline)
			}
		})
	}
}

func TestRemover_Remove_FailureBranches(t *testing.T) {
	removeErr := errors.New("connection refused")
	lookupErr := errors.New("no such client")
	decryptErr := errors.New("bad nonce")

	tests := []struct {
		name       string
		clients    ClientsLookup
		master     Decryptor
		lookup     PluginLookup
		wantReason string
		wantErr    error
	}{
		{
			name:       "client row missing",
			clients:    &fakeClients{err: lookupErr},
			master:     &fakeDecryptor{plain: []byte("{}")},
			lookup:     func(string) registry.Client { return nil },
			wantReason: ReasonLookup,
			wantErr:    lookupErr,
		},
		{
			name:       "plugin not installed",
			clients:    &fakeClients{client: &domain.Client{ClientName: "gone"}},
			master:     &fakeDecryptor{plain: []byte("{}")},
			lookup:     func(string) registry.Client { return nil },
			wantReason: ReasonNoPlugin,
		},
		{
			name:       "plugin cannot remove",
			clients:    &fakeClients{client: &domain.Client{ClientName: "downloadfolder"}},
			master:     &fakeDecryptor{plain: []byte("{}")},
			lookup:     func(string) registry.Client { return &fakeClient{name: "downloadfolder"} },
			wantReason: ReasonUnsupported,
		},
		{
			name:    "config undecryptable",
			clients: &fakeClients{client: &domain.Client{ClientName: "qbittorrent"}},
			master:  &fakeDecryptor{err: decryptErr},
			lookup: func(string) registry.Client {
				return &fakeRemovalClient{fakeClient: fakeClient{name: "qbittorrent"}}
			},
			wantReason: ReasonDecrypt,
			wantErr:    decryptErr,
		},
		{
			name:    "client rejected the call",
			clients: &fakeClients{client: &domain.Client{ClientName: "transmission"}},
			master:  &fakeDecryptor{plain: []byte("{}")},
			lookup: func(string) registry.Client {
				return &fakeRemovalClient{fakeClient: fakeClient{name: "transmission"}, err: removeErr}
			},
			wantReason: ReasonError,
			wantErr:    removeErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Remover{Clients: tt.clients, Master: tt.master, Lookup: tt.lookup, Timeout: time.Second}
			got := r.Remove(context.Background(), uuid.New(),
				map[uuid.UUID][]string{uuid.New(): {"aaa"}}, false)

			if len(got) != 1 {
				t.Fatalf("want 1 result, got %d", len(got))
			}
			if got[0].OK {
				t.Fatalf("want failure, got OK: %+v", got[0])
			}
			if got[0].Reason != tt.wantReason {
				t.Fatalf("want reason %q, got %q", tt.wantReason, got[0].Reason)
			}
			if tt.wantErr != nil && !errors.Is(got[0].Err, tt.wantErr) {
				t.Fatalf("want err %v, got %v", tt.wantErr, got[0].Err)
			}
			if len(got[0].Hashes) != 1 {
				t.Fatalf("failure result must still carry its hashes, got %v", got[0].Hashes)
			}
		})
	}
}

func TestGroupByClient_SkipsRowsWithoutAClient(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	deliveries := []*domain.TopicDelivery{
		{Infohash: "h1", ClientID: &a},
		{Infohash: "h2", ClientID: &a},
		{Infohash: "h3", ClientID: &b},
		{Infohash: "h4", ClientID: nil}, // unaddressable — no client to remove from
	}

	got := GroupByClient(deliveries)

	if len(got) != 2 {
		t.Fatalf("want 2 clients, got %d (%v)", len(got), got)
	}
	if len(got[a]) != 2 || len(got[b]) != 1 {
		t.Fatalf("grouping wrong: %v", got)
	}
}

func TestGroupByClient_EmptyInput(t *testing.T) {
	if got := GroupByClient(nil); len(got) != 0 {
		t.Fatalf("want empty map, got %v", got)
	}
}
