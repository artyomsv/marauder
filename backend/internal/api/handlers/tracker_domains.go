package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/audit"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/problem"
)

// domainsStore is the consumer seam over *domains.Store (issue #126).
type domainsStore interface {
	Get(name string) (active string, custom []string)
	Set(ctx context.Context, name, active string, custom []string) error
}

// TrackerDomains is the admin-only handler group for per-tracker domain
// configuration: list the known/custom/active domains for every
// WithDomains-capable tracker, update a tracker's active/custom domains,
// and test a candidate domain's reachability before it's saved.
type TrackerDomains struct {
	Store domainsStore
	// Probe checks whether host is reachable. Nil uses DefaultDomainProbe;
	// tests inject a fake to avoid touching the network.
	Probe   func(ctx context.Context, host string) error
	BaseURL string
	Audit   *audit.Logger
}

type trackerDomainsView struct {
	Name          string   `json:"name"`
	DisplayName   string   `json:"display_name"`
	DefaultDomain string   `json:"default_domain"`
	KnownDomains  []string `json:"known_domains"`
	CustomDomains []string `json:"custom_domains"`
	ActiveDomain  string   `json:"active_domain"`
}

type trackerDomainsReq struct {
	ActiveDomain  string   `json:"active_domain"`
	CustomDomains []string `json:"custom_domains"`
}

type testTrackerDomainReq struct {
	Domain string `json:"domain"`
}

// hostnameRe: RFC-1123 labels, at least two labels, no scheme/port/path.
var hostnameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

// validateHostname lowercases and validates a bare hostname (no scheme,
// port, path, or IP literal). It rejects anything that isn't a plain
// RFC-1123 domain name with at least two labels.
func validateHostname(h string) (string, error) {
	h = strings.ToLower(strings.TrimSpace(h))
	if h == "" {
		return "", errors.New("hostname is empty")
	}
	if net.ParseIP(h) != nil {
		return "", fmt.Errorf("hostname %q must not be an IP literal", h)
	}
	if !hostnameRe.MatchString(h) {
		return "", fmt.Errorf("hostname %q is not a valid domain name (no scheme, port or path)", h)
	}
	return h, nil
}

// List handles GET /system/trackers/domains (admin) — one entry per
// tracker plugin that implements registry.WithDomains.
func (h *TrackerDomains) List(w http.ResponseWriter, r *http.Request) {
	all := registry.ListTrackers()
	views := make([]trackerDomainsView, 0, len(all))
	for _, t := range all {
		wd, ok := t.(registry.WithDomains)
		if !ok {
			continue
		}
		views = append(views, h.toView(wd))
	}
	writeJSON(w, http.StatusOK, views)
}

// Update handles PUT /system/trackers/{name}/domains (admin). Validation
// order: tracker exists & implements WithDomains -> each custom hostname
// validated & lowercased -> active_domain in {""} u known u custom ->
// Store.Set -> audit.
func (h *TrackerDomains) Update(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	wd, perr := h.lookupTracker(name)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}

	var req trackerDomainsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid JSON"))
		return
	}

	custom := make([]string, 0, len(req.CustomDomains))
	for _, d := range req.CustomDomains {
		hn, err := validateHostname(d)
		if err != nil {
			problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable(err.Error()))
			return
		}
		custom = append(custom, hn)
	}

	active := strings.ToLower(strings.TrimSpace(req.ActiveDomain))
	if active != "" && !domainKnownOrCustom(active, wd.Domains(), custom) {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable(
			fmt.Sprintf("active_domain %q must be one of the tracker's known or custom domains", active)))
		return
	}

	if err := h.Store.Set(r.Context(), name, active, custom); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(err.Error()))
		return
	}

	uid, _ := currentUserID(r)
	h.audit(nilIfNil(uid), "tracker.domains.update", name,
		map[string]any{"active_domain": active, "custom_domains": custom})

	writeJSON(w, http.StatusOK, h.toView(wd))
}

// Test handles POST /system/trackers/{name}/domains/test (admin) — probes
// reachability of a candidate domain without persisting it. Always 200
// with {ok, detail} once the hostname itself is valid; an invalid
// hostname is a 422 (nothing to probe).
func (h *TrackerDomains) Test(w http.ResponseWriter, r *http.Request) {
	var req testTrackerDomainReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid JSON"))
		return
	}
	hn, err := validateHostname(req.Domain)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable(err.Error()))
		return
	}

	probe := h.Probe
	if probe == nil {
		probe = DefaultDomainProbe
	}
	if err := probe(r.Context(), hn); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "detail": ""})
}

// DefaultDomainProbe is the production Probe: it pre-checks DNS (rejecting
// a host whose resolved IPs are ALL loopback/private/link-local — the
// same non-routable-IP shape as lostfilm's validateRedirectURL, copied
// rather than imported so this handler has no dependency on a tracker
// plugin package) and then performs a bounded GET to confirm the host
// actually answers over HTTPS.
func DefaultDomainProbe(ctx context.Context, host string) error {
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("DNS lookup for %q failed: %w", host, err)
	}
	routable := false
	for _, ip := range ips {
		if !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()) {
			routable = true
			break
		}
	}
	if !routable {
		return fmt.Errorf("host %q resolves only to non-routable IPs", host)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/", nil)
	if err != nil {
		return fmt.Errorf("building request for %q: %w", host, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GET https://%s/ failed: %w", host, err)
	}
	defer resp.Body.Close()
	return nil
}

// lookupTracker resolves a WithDomains-capable tracker by name, returning
// a 404 problem both when the name is unknown and when the tracker exists
// but doesn't support domain configuration.
func (h *TrackerDomains) lookupTracker(name string) (registry.WithDomains, *problem.Error) {
	t := registry.GetTracker(name)
	if t == nil {
		return nil, problem.ErrNotFound("tracker not found")
	}
	wd, ok := t.(registry.WithDomains)
	if !ok {
		return nil, problem.ErrNotFound("tracker does not support domain configuration")
	}
	return wd, nil
}

func (h *TrackerDomains) toView(wd registry.WithDomains) trackerDomainsView {
	known := wd.Domains()
	active, custom := h.Store.Get(wd.Name())
	if custom == nil {
		custom = []string{}
	}
	defaultDomain := ""
	if len(known) > 0 {
		defaultDomain = known[0]
	}
	return trackerDomainsView{
		Name:          wd.Name(),
		DisplayName:   wd.DisplayName(),
		DefaultDomain: defaultDomain,
		KnownDomains:  known,
		CustomDomains: custom,
		ActiveDomain:  active,
	}
}

func (h *TrackerDomains) audit(uid *uuid.UUID, action, resourceID string, meta map[string]any) {
	if h.Audit == nil {
		return
	}
	h.Audit.Generic(uid, action, "tracker", resourceID, "success", meta)
}

// domainKnownOrCustom reports whether host (already lowercased) matches
// one of the tracker's known domains or the (already-lowercased) custom
// list.
func domainKnownOrCustom(host string, known, custom []string) bool {
	for _, d := range known {
		if host == strings.ToLower(d) {
			return true
		}
	}
	for _, d := range custom {
		if host == d {
			return true
		}
	}
	return false
}
