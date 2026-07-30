package handlers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// --- fakes for the status endpoint -------------------------------------

type fakeDeliveriesStore struct {
	items []*domain.TopicDelivery

	deleted   bool
	deleteErr error
}

func (f *fakeDeliveriesStore) ListForTopic(context.Context, uuid.UUID) ([]*domain.TopicDelivery, error) {
	return f.items, nil
}

func (f *fakeDeliveriesStore) DeleteForTopic(context.Context, uuid.UUID) (int64, error) {
	f.deleted = true
	return int64(len(f.items)), f.deleteErr
}

type fakeClientsLookup struct{ client *domain.Client }

func (f *fakeClientsLookup) GetByID(context.Context, uuid.UUID, uuid.UUID) (*domain.Client, error) {
	return f.client, nil
}
func (f *fakeClientsLookup) GetDefault(context.Context, uuid.UUID) (*domain.Client, error) {
	return f.client, nil
}

type passthroughDecryptor struct{}

func (passthroughDecryptor) Decrypt(ct, _ []byte) ([]byte, error) { return ct, nil }

// statusClientSingleton is registered once (registry is global) and its
// canned statuses are set per-test before the call.
type fakeStatusClient struct {
	name     string
	statuses []registry.TorrentStatus
}

func (f *fakeStatusClient) Name() string                 { return f.name }
func (f *fakeStatusClient) DisplayName() string          { return f.name }
func (f *fakeStatusClient) ConfigSchema() map[string]any { return nil }
func (f *fakeStatusClient) Test(context.Context, []byte) error {
	return nil
}
func (f *fakeStatusClient) Add(context.Context, []byte, *domain.Payload, domain.AddOptions) error {
	return nil
}
func (f *fakeStatusClient) Status(_ context.Context, _ []byte, _ []string) ([]registry.TorrentStatus, error) {
	return f.statuses, nil
}

const fakeStatusClientName = "fakestatusclient"

var statusClientSingleton = &fakeStatusClient{name: fakeStatusClientName}

func init() {
	registry.RegisterClient(statusClientSingleton)
}

// statusResp mirrors the JSON the Status handler emits.
type statusResp struct {
	ClientSupportsStatus bool `json:"client_supports_status"`
	Deliveries           []struct {
		Label       string    `json:"label"`
		Infohash    string    `json:"infohash"`
		DeliveredAt time.Time `json:"delivered_at"`
		State       string    `json:"state"`
		PercentDone *float64  `json:"percent_done"`
	} `json:"deliveries"`
}

func TestTopicsStatus_LiveAugmentsDeliveries(t *testing.T) {
	topicID := uuid.New()
	clientID := uuid.New()
	store := &fakeTopicStore{getByID: &domain.Topic{
		ID: topicID, ClientID: &clientID, TrackerName: "x", DisplayName: "Show",
	}}
	deliveries := &fakeDeliveriesStore{items: []*domain.TopicDelivery{
		{Infohash: "aaa", Label: "s01e01", DeliveredAt: time.Now()},
		{Infohash: "bbb", Label: "s01e02", DeliveredAt: time.Now()},
	}}
	// Live status for "aaa" only (uppercase to prove the handler lower-cases
	// before matching); "bbb" is unknown to the client → stays "delivered".
	statusClientSingleton.statuses = []registry.TorrentStatus{
		{Hash: "AAA", PercentDone: 0.5, State: registry.StateDownloading},
	}
	t.Cleanup(func() { statusClientSingleton.statuses = nil })

	h := &Topics{
		Topics:     store,
		Deliveries: deliveries,
		Clients:    &fakeClientsLookup{client: &domain.Client{ID: clientID, ClientName: fakeStatusClientName}},
		Master:     passthroughDecryptor{},
		BaseURL:    "http://test",
	}

	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uuid.New(), nil), "id", topicID.String())
	h.Status(w, req)

	if w.Code != 200 {
		t.Fatalf("status %d, want 200; body %s", w.Code, w.Body.String())
	}
	var resp statusResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.ClientSupportsStatus {
		t.Error("expected client_supports_status=true")
	}
	if len(resp.Deliveries) != 2 {
		t.Fatalf("got %d deliveries, want 2", len(resp.Deliveries))
	}
	a := resp.Deliveries[0]
	if a.Infohash != "aaa" || a.State != registry.StateDownloading || a.PercentDone == nil || *a.PercentDone != 0.5 {
		t.Errorf("aaa item = %+v, want downloading @ 0.5", a)
	}
	b := resp.Deliveries[1]
	if b.Infohash != "bbb" || b.State != "delivered" || b.PercentDone != nil {
		t.Errorf("bbb item = %+v, want state=delivered, percent=nil", b)
	}
}

func TestTopicsStatus_NoCapability_DeliveredOnly(t *testing.T) {
	topicID := uuid.New()
	store := &fakeTopicStore{getByID: &domain.Topic{ID: topicID, DisplayName: "Show"}}
	deliveries := &fakeDeliveriesStore{items: []*domain.TopicDelivery{
		{Infohash: "ccc", Label: "Some.Release.1080p", DeliveredAt: time.Now()},
	}}
	// No client wired (and no default) → liveStatus returns false/empty.
	h := &Topics{
		Topics:     store,
		Deliveries: deliveries,
		Clients:    &fakeClientsLookup{client: nil},
		Master:     passthroughDecryptor{},
		BaseURL:    "http://test",
	}

	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uuid.New(), nil), "id", topicID.String())
	h.Status(w, req)

	if w.Code != 200 {
		t.Fatalf("status %d, want 200; body %s", w.Code, w.Body.String())
	}
	var resp statusResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ClientSupportsStatus {
		t.Error("expected client_supports_status=false")
	}
	if len(resp.Deliveries) != 1 || resp.Deliveries[0].State != "delivered" {
		t.Errorf("deliveries = %+v, want one delivered item", resp.Deliveries)
	}
}
