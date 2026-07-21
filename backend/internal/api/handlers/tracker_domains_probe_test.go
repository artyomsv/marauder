package handlers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestIsRoutableIP(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want bool
	}{
		{"loopback v4", "127.0.0.1", false},
		{"loopback v6", "::1", false},
		{"rfc1918 10.x", "10.0.0.1", false},
		{"rfc1918 172.16.x", "172.16.0.5", false},
		{"rfc1918 192.168.x", "192.168.1.1", false},
		{"link-local v4 metadata IP", "169.254.169.254", false},
		{"link-local v4", "169.254.1.1", false},
		{"link-local v6", "fe80::1", false},
		{"unique local v6 fc00::/7 (fc)", "fc00::1", false},
		{"unique local v6 fc00::/7 (fd)", "fd12:3456:789a::1", false},
		{"unspecified v4", "0.0.0.0", false},
		{"unspecified v6", "::", false},
		{"public v4", "8.8.8.8", true},
		{"public v6", "2001:4860:4860::8888", true},
		{"ipv4-mapped loopback", "::ffff:127.0.0.1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("net.ParseIP(%q) returned nil", tt.ip)
			}
			if got := isRoutableIP(ip); got != tt.want {
				t.Errorf("isRoutableIP(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestFirstRoutableIP(t *testing.T) {
	tests := []struct {
		name    string
		ips     []string
		want    string
		wantErr bool
	}{
		{"empty", nil, "", true},
		{"all private", []string{"10.0.0.1", "192.168.1.1"}, "", true},
		{"all blocked mixed families", []string{"127.0.0.1", "fe80::1", "169.254.169.254"}, "", true},
		{"single public", []string{"8.8.8.8"}, "8.8.8.8", false},
		{
			// The candidate order matters: firstRoutableIP must skip the
			// blocked candidate and pick the routable one that follows it
			// — not fall back to "first in the list regardless".
			"public follows private",
			[]string{"10.0.0.1", "8.8.8.8"},
			"8.8.8.8",
			false,
		},
		{
			"private follows public",
			[]string{"8.8.8.8", "10.0.0.1"},
			"8.8.8.8",
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ips := make([]net.IP, len(tt.ips))
			for i, s := range tt.ips {
				ip := net.ParseIP(s)
				if ip == nil {
					t.Fatalf("net.ParseIP(%q) returned nil", s)
				}
				ips[i] = ip
			}
			got, err := firstRoutableIP(ips)
			if tt.wantErr {
				if !errors.Is(err, errBlockedAddress) {
					t.Fatalf("err = %v, want errBlockedAddress", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.String() != tt.want {
				t.Errorf("firstRoutableIP() = %s, want %s", got, tt.want)
			}
		})
	}
}

// TestDenyRedirects_StopsAtFirstResponse verifies the probe client's
// CheckRedirect at the client-construction level: build a client wired
// with the exact same denyRedirects value the production probe uses, and
// confirm it never issues a second request to the redirect target.
//
// This is deliberately NOT a full DefaultDomainProbe(ctx, host) call: the
// only reachable "attacker" server in a test is httptest's own loopback
// listener, and the probe's dial-time IP-vetting gate (vettedDialContext)
// would itself refuse to dial a loopback target — which would make a
// redirect-following bug and a healthy vetting gate indistinguishable by
// the test's outcome (both end in an error). Testing CheckRedirect in
// isolation, with the production denyRedirects func plugged into a
// throwaway client, isolates exactly the behavior finding #1 is about:
// once a response is in hand, a 3xx is never chased further.
func TestDenyRedirects_StopsAtFirstResponse(t *testing.T) {
	var privateHits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/private", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&privateHits, 1)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/private", http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{CheckRedirect: denyRedirects}
	resp, err := client.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want %d (a 3xx counts as reachable and must not be followed)", resp.StatusCode, http.StatusFound)
	}
	if got := atomic.LoadInt32(&privateHits); got != 0 {
		t.Errorf("redirect target was requested %d time(s), want 0", got)
	}
}

// fakeTimeoutErr implements net.Error with Timeout()==true, for testing
// classifyProbeError's timeout branch without a real network call.
type fakeTimeoutErr struct{}

func (fakeTimeoutErr) Error() string   { return "i/o timeout" }
func (fakeTimeoutErr) Timeout() bool   { return true }
func (fakeTimeoutErr) Temporary() bool { return true }

func TestClassifyProbeError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			"dns error",
			fmt.Errorf("dns lookup for %q failed: %w", "x.example", &net.DNSError{Err: "no such host", Name: "x.example", IsNotFound: true}),
			"dns lookup failed",
		},
		{
			"blocked address",
			fmt.Errorf("host %q: %w", "x.example", errBlockedAddress),
			"blocked address",
		},
		{
			"context deadline exceeded",
			fmt.Errorf("GET https://x.example/ failed: %w", context.DeadlineExceeded),
			"timeout",
		},
		{
			"net.Error timeout",
			fmt.Errorf("dial: %w", fakeTimeoutErr{}),
			"timeout",
		},
		{
			"net.OpError",
			fmt.Errorf("dial: %w", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}),
			"connection failed",
		},
		{
			"unclassified generic error",
			errors.New("unreachable"),
			"unreachable",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyProbeError(tt.err); got != tt.want {
				t.Errorf("classifyProbeError() = %q, want %q", got, tt.want)
			}
		})
	}
}
