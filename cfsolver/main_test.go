package main

import (
	"strings"
	"testing"

	"github.com/chromedp/cdproto/network"
)

func TestIsChallengePage(t *testing.T) {
	tests := []struct {
		name string
		html string
		want bool
	}{
		{
			// The exact title the solver observed while RuTracker was still
			// challenging (title="Just a moment...", ~27 KB of interstitial).
			name: "cloudflare interstitial title",
			html: `<html><head><title>Just a moment...</title></head><body></body></html>`,
			want: true,
		},
		{
			name: "legacy browser-verification markup",
			html: `<div class="cf-browser-verification cf-im-under-attack">`,
			want: true,
		},
		{
			name: "challenge form",
			html: `<form id="challenge-form" action="/cdn-cgi/l/chk_jschl">`,
			want: true,
		},
		{
			name: "challenge options blob",
			html: `<script>window._cf_chl_opt={cvId:"3"};</script>`,
			want: true,
		},
		{
			name: "running-challenge container",
			html: `<div id="cf-challenge-running">`,
			want: true,
		},
		{
			// A real forum page must not be mistaken for a challenge, or the
			// solver would poll until timeout on a page that already loaded.
			name: "destination forum page is not a challenge",
			html: `<html><head><title>RuTracker.org</title></head><body><div id="logged-in-username">Nossp</div></body></html>`,
			want: false,
		},
		{
			name: "empty document is not a challenge",
			html: "",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isChallengePage(tt.html); got != tt.want {
				t.Errorf("isChallengePage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasClearance(t *testing.T) {
	tests := []struct {
		name    string
		cookies []*network.Cookie
		want    bool
	}{
		{
			name:    "no cookies at all",
			cookies: nil,
			want:    false,
		},
		{
			// The pre-fix failure mode: a challenge page whose cookies carry
			// no clearance. Treating this as success is what made the solver
			// report ok:true while having accomplished nothing.
			name:    "unrelated cookies only",
			cookies: []*network.Cookie{{Name: "bb_guid"}, {Name: "opt_js"}},
			want:    false,
		},
		{
			name:    "cf_clearance present",
			cookies: []*network.Cookie{{Name: "bb_guid"}, {Name: "cf_clearance"}},
			want:    true,
		},
		{
			// __cf_bm is bot-management telemetry that Cloudflare sets on the
			// interstitial itself, so it says nothing about whether the
			// challenge was passed. Treating it as clearance would let the
			// poll exit while still on the challenge page and report ok:true
			// — precisely the false-success bug this service was fixed to
			// stop doing. Only cf_clearance is proof.
			name:    "bot-management cookie is not clearance",
			cookies: []*network.Cookie{{Name: "__cf_bm"}},
			want:    false,
		},
		{
			name:    "clearance still detected alongside the bot cookie",
			cookies: []*network.Cookie{{Name: "__cf_bm"}, {Name: "cf_clearance"}},
			want:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasClearance(tt.cookies); got != tt.want {
				t.Errorf("hasClearance() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDefaultUserAgent_NotHeadless pins the fix that actually made RuTracker
// solvable: Chromium's stock UA advertises "HeadlessChrome", which Cloudflare
// fingerprints directly, and the challenge never cleared while it did.
func TestDefaultUserAgent_NotHeadless(t *testing.T) {
	if strings.Contains(defaultUserAgent, "Headless") {
		t.Errorf("defaultUserAgent must not advertise HeadlessChrome, got %q", defaultUserAgent)
	}
	if !strings.Contains(defaultUserAgent, "Chrome/") {
		t.Errorf("defaultUserAgent should still look like Chrome, got %q", defaultUserAgent)
	}
}
