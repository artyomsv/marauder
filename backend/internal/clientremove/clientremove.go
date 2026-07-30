// Package clientremove removes torrents from a download client by infohash.
//
// Two callers need the identical sequence — resolve the client row, resolve its
// plugin, assert registry.WithRemoval, decrypt the stored config blob, and call
// Remove under a bounded timeout: the scheduler's replace-on-update policy
// (issue #101) and the topic reset endpoint. It lives here so there is one
// implementation rather than two that drift apart.
//
// The package deliberately does not log or meter. Its two callers report under
// different metric names and log components, so they label their own.
package clientremove

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// Reason classifies why a removal did not happen. Empty on success. These
// strings are used as Prometheus label values, so they are a closed set.
const (
	ReasonLookup      = "lookup"      // the client row could not be loaded
	ReasonNoPlugin    = "no_plugin"   // the client's plugin is not installed
	ReasonUnsupported = "unsupported" // the plugin cannot remove torrents
	ReasonDecrypt     = "decrypt"     // the stored config blob is unreadable
	ReasonError       = "error"       // the client rejected or failed the call
)

// ClientLabel keeps metric and log cardinality bounded when the client row
// could not be loaded and its name is therefore unknown. Both callers label
// their own metrics from Result.ClientName, so the fallback lives here rather
// than being spelled out once per caller.
func ClientLabel(name string) string {
	if name == "" {
		return "unknown"
	}
	return name
}

// ClientsLookup resolves a client row scoped to its owner.
type ClientsLookup interface {
	GetByID(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*domain.Client, error)
}

// Decryptor unseals a stored client config blob.
type Decryptor interface {
	Decrypt(ct, nonce []byte) ([]byte, error)
}

// PluginLookup resolves a client plugin by name. A test seam — production
// passes registry.GetClient.
type PluginLookup func(name string) registry.Client

// Result is the outcome of removing one client's share of the hashes.
type Result struct {
	ClientID   uuid.UUID
	ClientName string   // empty when the client row could not be loaded
	Hashes     []string // the hashes this call covered, success or not
	OK         bool     // true only when the client confirmed the removal
	Reason     string   // "" when OK; one of the Reason* constants otherwise
	Err        error    // the underlying error, when there was one
}

// Remover removes torrents from clients.
type Remover struct {
	Clients ClientsLookup
	Master  Decryptor
	Lookup  PluginLookup
	// Timeout bounds each per-client Remove call. Zero means "inherit the
	// caller's context deadline" — context.WithTimeout(ctx, 0) yields an
	// already-expired context, which would fail every removal instantly.
	Timeout time.Duration
}

// New constructs a Remover backed by the global plugin registry.
func New(clients ClientsLookup, master Decryptor, timeout time.Duration) *Remover {
	return &Remover{Clients: clients, Master: master, Lookup: registry.GetClient, Timeout: timeout}
}

// Remove deletes the given infohashes from each client that holds them,
// optionally deleting their on-disk data. byClient maps a client id to the
// hashes delivered through it — deliveries are grouped by the client they
// actually went to, which may differ from the topic's current client if the
// user reassigned it.
//
// Returns one Result per client, in unspecified order. It never returns an
// error: every failure is reported per client so the caller decides whether to
// degrade or abort.
func (r *Remover) Remove(ctx context.Context, userID uuid.UUID, byClient map[uuid.UUID][]string, deleteData bool) []Result {
	out := make([]Result, 0, len(byClient))
	for clientID, hashes := range byClient {
		out = append(out, r.removeOne(ctx, userID, clientID, hashes, deleteData))
	}
	return out
}

func (r *Remover) removeOne(ctx context.Context, userID, clientID uuid.UUID, hashes []string, deleteData bool) Result {
	res := Result{ClientID: clientID, Hashes: hashes}

	cfg, err := r.Clients.GetByID(ctx, clientID, userID)
	if err != nil {
		res.Reason, res.Err = ReasonLookup, err
		return res
	}
	res.ClientName = cfg.ClientName

	plugin := r.Lookup(cfg.ClientName)
	if plugin == nil {
		res.Reason = ReasonNoPlugin
		return res
	}
	remover, ok := plugin.(registry.WithRemoval)
	if !ok {
		res.Reason = ReasonUnsupported
		return res
	}

	rawConfig, err := r.Master.Decrypt(cfg.ConfigEnc, cfg.ConfigNonce)
	if err != nil {
		res.Reason, res.Err = ReasonDecrypt, err
		return res
	}

	rctx := ctx
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		rctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}
	if err := remover.Remove(rctx, rawConfig, hashes, deleteData); err != nil {
		res.Reason, res.Err = ReasonError, err
		return res
	}
	res.OK = true
	return res
}

// GroupByClient buckets deliveries by the client they were sent to. Rows with
// no client id are skipped — there is no client to address the removal to.
func GroupByClient(deliveries []*domain.TopicDelivery) map[uuid.UUID][]string {
	byClient := map[uuid.UUID][]string{}
	for _, d := range deliveries {
		if d.ClientID == nil {
			continue
		}
		byClient[*d.ClientID] = append(byClient[*d.ClientID], d.Infohash)
	}
	return byClient
}
