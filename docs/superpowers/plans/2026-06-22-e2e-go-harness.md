# Go full-stack E2E harness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the inline-bash full-stack e2e in `.github/workflows/e2e.yml` with a table-driven Go harness that drives the live stack over HTTP from inside the compose network.

**Architecture:** A new black-box system-test package `backend/e2e` (build tag `e2e`) drives the already-running Marauder stack and qBittorrent over HTTP. It does **not** reuse `e2etest.RunFullPipeline` (the in-process fake runner) — different layer. CI brings the stack up (unchanged) and runs `go test -tags=e2e` in a `golang:1.25` container joined to the `deploy_default` network, reaching services by Docker DNS (`gateway:6688`, `qbittorrent:6611`).

**Tech Stack:** Go 1.25, stdlib `net/http` + `testing` only (no new deps), GitHub Actions, docker-compose.

## Global Constraints

- All harness Go files except `doc.go` carry `//go:build e2e`; the package must be invisible to `go test ./...` and `go build ./...` without the tag.
- No new test dependencies — stdlib `net/http` + `testing` only (manual clients, no frameworks).
- Go: tabs for indentation, `gofmt` mandatory, errors wrapped with context, `MixedCaps` naming.
- No-synthetic-data: infohash is an obviously-synthetic 40-hex value (`deadbeef`-prefixed); category `sonarr-e2e` — test markers only.
- Commit type `test(e2e):` (the auto-release flow does NOT bump on `test`/`ci`/`chore` — do not use `feat`/`fix`/`perf`).
- Marauder API base in CI: `http://gateway:6688`; qBittorrent: `http://qbittorrent:6611`.
- Docker network name `deploy_default` (compose project `deploy`, no `-p`). If `e2e.yml` ever adds `-p`, the `--network` value must change in lockstep.

## File Structure

```
backend/e2e/
├── doc.go             // NO build tag — package decl + doc, keeps untagged build clean
├── harness_test.go    // //go:build e2e — config, readEnv, envOr, uniqueInfohash, magnet, pollUntil
├── marauder_test.go   // //go:build e2e — Marauder API client: Login, CreateQbitClient, CreateTopic, StatusHasInfohash
├── qbit_test.go       // //go:build e2e — qBittorrent API client: Login, TorrentInfo
└── delivery_test.go   // //go:build e2e — TestDelivery (table-driven scenario)
```

- Helpers live in `_test.go` files so the package compiles only under `go test`. `doc.go` (no tag) makes the untagged package a valid empty package (no "build constraints exclude all Go files" noise).
- `.github/workflows/e2e.yml` — modified (swap inline walkthrough for the Go harness step).
- `docs/test-e2e-magnet.md` — modified (pointer to the automated harness).

**Verification note (read before starting):** This package *is* a test; its helpers are thin HTTP wrappers whose real verification is the live e2e run, not unit tests of the wrappers (writing fakes to test the test client would be YAGNI). Per-task verification is therefore: (a) it compiles under `-tags=e2e`, (b) it is invisible without the tag, (c) `gofmt`/`go vet -tags=e2e` clean. The behavioural green is the live dry-run in Task 5 and the CI run after Task 6.

---

### Task 1: Package scaffold, build-tag isolation, config + shared helpers

**Files:**
- Create: `backend/e2e/doc.go`
- Create: `backend/e2e/harness_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type config struct { BaseURL, AdminUser, AdminPass, QbitURL, QbitUser, QbitPassword string }`
  - `func readEnv(t *testing.T) config` — skips when `MARAUDER_BASE_URL` unset
  - `func uniqueInfohash(t *testing.T) string` — 40-hex synthetic, stable per test name
  - `func magnet(infohash string) string`
  - `func pollUntil(interval, timeout time.Duration, fn func() bool) bool`

- [ ] **Step 1: Create `backend/e2e/doc.go`** (no build tag)

```go
// Package e2e holds Marauder's full-stack end-to-end tests. The tests drive a
// running Marauder stack (and a real qBittorrent) over HTTP and are guarded by
// the "e2e" build tag, so they run only via `go test -tags=e2e ./e2e/...` and
// never during the normal `go test ./...` suite.
package e2e
```

- [ ] **Step 2: Create `backend/e2e/harness_test.go`**

```go
//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"testing"
	"time"
)

type config struct {
	BaseURL      string
	AdminUser    string
	AdminPass    string
	QbitURL      string
	QbitUser     string
	QbitPassword string
}

// readEnv loads the e2e config from the environment. If MARAUDER_BASE_URL is
// unset the whole suite is skipped, so a stray local `go test -tags=e2e` is
// inert rather than a hard failure.
func readEnv(t *testing.T) config {
	t.Helper()
	base := os.Getenv("MARAUDER_BASE_URL")
	if base == "" {
		t.Skip("MARAUDER_BASE_URL unset; skipping full-stack e2e")
	}
	return config{
		BaseURL:      base,
		AdminUser:    envOr("MARAUDER_ADMIN_USER", "admin"),
		AdminPass:    envOr("MARAUDER_ADMIN_PASS", "pleasechangeme"),
		QbitURL:      envOr("QBIT_URL", "http://qbittorrent:6611"),
		QbitUser:     envOr("QBIT_USER", "admin"),
		QbitPassword: os.Getenv("QBIT_PASSWORD"),
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// uniqueInfohash returns an obviously-synthetic 40-hex-char infohash that is
// stable per test name, so multiple table rows do not collide in qBittorrent.
// The "deadbeef" prefix marks it as a test value; it is never a real torrent.
func uniqueInfohash(t *testing.T) string {
	t.Helper()
	var sum uint32
	for _, r := range t.Name() {
		sum = sum*31 + uint32(r)
	}
	return fmt.Sprintf("deadbeef%032x", sum)
}

func magnet(infohash string) string {
	return "magnet:?xt=urn:btih:" + infohash + "&dn=marauder-e2e"
}

// pollUntil calls fn every interval until it returns true or timeout elapses.
func pollUntil(interval, timeout time.Duration, fn func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if fn() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(interval)
	}
}
```

- [ ] **Step 3: Verify the package is invisible without the tag**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go build ./... && go vet ./e2e/... && go test ./e2e/..."`
Expected: build succeeds; `go test ./e2e/...` prints `?   github.com/artyomsv/marauder/backend/e2e   [no test files]` (the `_test.go` files are excluded without the tag).

- [ ] **Step 4: Verify it compiles WITH the tag**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go vet -tags=e2e ./e2e/... && gofmt -l e2e/"`
Expected: no output (vet clean, gofmt clean). It will not *run* anything yet — no `Test*` functions exist.

- [ ] **Step 5: Commit**

```bash
git add backend/e2e/doc.go backend/e2e/harness_test.go
git commit -m "test(e2e): scaffold build-tagged e2e package with config + helpers"
```

---

### Task 2: Marauder API client

**Files:**
- Create: `backend/e2e/marauder_test.go`

**Interfaces:**
- Consumes: `config` (Task 1).
- Produces:
  - `func newMarauder(cfg config) *marauderClient`
  - `func (m *marauderClient) Login(t *testing.T)`
  - `func (m *marauderClient) CreateQbitClient(t *testing.T, downloadDir string) string` — returns client id
  - `func (m *marauderClient) CreateTopic(t *testing.T, url, clientID, category string) string` — returns topic id
  - `func (m *marauderClient) StatusHasInfohash(t *testing.T, topicID, infohash string) bool`

- [ ] **Step 1: Create `backend/e2e/marauder_test.go`**

```go
//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type marauderClient struct {
	cfg   config
	token string
	hc    *http.Client
}

func newMarauder(cfg config) *marauderClient {
	return &marauderClient{cfg: cfg, hc: &http.Client{Timeout: 30 * time.Second}}
}

// do performs a JSON request against the Marauder API, attaches the bearer
// token when present, and fails the test on any non-2xx response.
func (m *marauderClient) do(t *testing.T, method, path string, body any) []byte {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal %s %s: %v", method, path, err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, strings.TrimRight(m.cfg.BaseURL, "/")+path, rdr)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if m.token != "" {
		req.Header.Set("Authorization", "Bearer "+m.token)
	}
	resp, err := m.hc.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("%s %s -> HTTP %d: %s", method, path, resp.StatusCode, string(data))
	}
	return data
}

func (m *marauderClient) Login(t *testing.T) {
	t.Helper()
	data := m.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"username": m.cfg.AdminUser,
		"password": m.cfg.AdminPass,
	})
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decode login: %v (%s)", err, string(data))
	}
	if out.AccessToken == "" {
		t.Fatalf("login returned empty access_token: %s", string(data))
	}
	m.token = out.AccessToken
}

// CreateQbitClient creates a qBittorrent client with a known base download_dir
// (so the resulting save path is deterministic) and returns its id.
func (m *marauderClient) CreateQbitClient(t *testing.T, downloadDir string) string {
	t.Helper()
	clientCfg := map[string]string{
		"url":          m.cfg.QbitURL,
		"username":     m.cfg.QbitUser,
		"password":     m.cfg.QbitPassword,
		"download_dir": downloadDir,
	}
	data := m.do(t, http.MethodPost, "/api/v1/clients", map[string]any{
		"client_name":  "qbittorrent",
		"display_name": "E2E qBit",
		"is_default":   true,
		"config":       clientCfg,
	})
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.ID == "" {
		t.Fatalf("decode create-client: %v (%s)", err, string(data))
	}
	return out.ID
}

// CreateTopic adds a topic and returns its id. domain.Topic has no JSON tags,
// so the id field marshals as "ID".
func (m *marauderClient) CreateTopic(t *testing.T, url, clientID, category string) string {
	t.Helper()
	data := m.do(t, http.MethodPost, "/api/v1/topics", map[string]any{
		"url":       url,
		"client_id": clientID,
		"category":  category,
	})
	var out struct {
		ID string `json:"ID"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.ID == "" {
		t.Fatalf("decode create-topic: %v (%s)", err, string(data))
	}
	return out.ID
}

// StatusHasInfohash reports whether the topic's status endpoint lists a
// delivery for infohash (case-insensitive).
func (m *marauderClient) StatusHasInfohash(t *testing.T, topicID, infohash string) bool {
	t.Helper()
	data := m.do(t, http.MethodGet, fmt.Sprintf("/api/v1/topics/%s/status", topicID), nil)
	var out struct {
		Deliveries []struct {
			Infohash string `json:"infohash"`
		} `json:"deliveries"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decode status: %v (%s)", err, string(data))
	}
	for _, d := range out.Deliveries {
		if strings.EqualFold(d.Infohash, infohash) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Verify it compiles with the tag and is invisible without it**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go vet -tags=e2e ./e2e/... && gofmt -l e2e/ && go build ./..."`
Expected: no output from vet/gofmt; `go build ./...` succeeds.

- [ ] **Step 3: Commit**

```bash
git add backend/e2e/marauder_test.go
git commit -m "test(e2e): add Marauder API client for the harness"
```

---

### Task 3: qBittorrent API client

**Files:**
- Create: `backend/e2e/qbit_test.go`

**Interfaces:**
- Consumes: `config` (Task 1).
- Produces:
  - `func newQbit(t *testing.T, cfg config) *qbitClient`
  - `func (q *qbitClient) Login(t *testing.T)`
  - `type qbitTorrent struct { Category, SavePath, State string }`
  - `func (q *qbitClient) TorrentInfo(t *testing.T, infohash string) (qbitTorrent, bool)`

- [ ] **Step 1: Create `backend/e2e/qbit_test.go`**

```go
//go:build e2e

package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"
)

type qbitClient struct {
	cfg config
	hc  *http.Client
}

func newQbit(t *testing.T, cfg config) *qbitClient {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &qbitClient{cfg: cfg, hc: &http.Client{Jar: jar, Timeout: 30 * time.Second}}
}

// Login authenticates against the qBittorrent WebUI; the SID cookie is stored
// in the client's jar for subsequent calls. qBittorrent answers 200 "Ok."
// (<=5.1) or 204 No Content (>=5.2) on success.
func (q *qbitClient) Login(t *testing.T) {
	t.Helper()
	resp, err := q.hc.PostForm(strings.TrimRight(q.cfg.QbitURL, "/")+"/api/v2/auth/login",
		url.Values{"username": {q.cfg.QbitUser}, "password": {q.cfg.QbitPassword}})
	if err != nil {
		t.Fatalf("qbit login: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("qbit login HTTP %d: %s", resp.StatusCode, string(body))
	}
}

type qbitTorrent struct {
	Category string `json:"category"`
	SavePath string `json:"save_path"`
	State    string `json:"state"`
}

// TorrentInfo returns the qBittorrent record for infohash and whether it was
// found. An unknown hash yields an empty 200 array, i.e. (zero, false).
func (q *qbitClient) TorrentInfo(t *testing.T, infohash string) (qbitTorrent, bool) {
	t.Helper()
	resp, err := q.hc.Get(strings.TrimRight(q.cfg.QbitURL, "/") +
		"/api/v2/torrents/info?hashes=" + infohash)
	if err != nil {
		t.Fatalf("qbit info: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("qbit info HTTP %d: %s", resp.StatusCode, string(data))
	}
	var arr []qbitTorrent
	if err := json.Unmarshal(data, &arr); err != nil {
		t.Fatalf("decode qbit info: %v (%s)", err, string(data))
	}
	if len(arr) == 0 {
		return qbitTorrent{}, false
	}
	return arr[0], true
}
```

- [ ] **Step 2: Verify compile + gofmt + invisible-without-tag**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go vet -tags=e2e ./e2e/... && gofmt -l e2e/ && go build ./..."`
Expected: no output from vet/gofmt; `go build ./...` succeeds.

- [ ] **Step 3: Commit**

```bash
git add backend/e2e/qbit_test.go
git commit -m "test(e2e): add qBittorrent API client for the harness"
```

---

### Task 4: The delivery test (table-driven)

**Files:**
- Create: `backend/e2e/delivery_test.go`

**Interfaces:**
- Consumes: `readEnv`, `uniqueInfohash`, `magnet`, `pollUntil` (Task 1); `newMarauder`, `Login`, `CreateQbitClient`, `CreateTopic`, `StatusHasInfohash` (Task 2); `newQbit`, `qbitClient.Login`, `qbitTorrent`, `TorrentInfo` (Task 3).
- Produces: `func TestDelivery(t *testing.T)` — the runnable e2e scenario.

- [ ] **Step 1: Create `backend/e2e/delivery_test.go`**

```go
//go:build e2e

package e2e

import (
	"testing"
	"time"
)

// TestDelivery drives the full pipeline: create a qBittorrent client and a
// magnet topic with a category, wait for the scheduler to push it, then assert
// the delivery was recorded (via Marauder) and that qBittorrent set the native
// category and save path (issue #75).
func TestDelivery(t *testing.T) {
	env := readEnv(t) // skips if MARAUDER_BASE_URL unset

	m := newMarauder(env)
	m.Login(t)
	clientID := m.CreateQbitClient(t, "/downloads")

	q := newQbit(t, env)
	q.Login(t)

	cases := []struct {
		name             string
		category         string
		wantSavePath     string
		wantQbitCategory string
	}{
		{
			name:             "category sets qbit label and nests save path",
			category:         "sonarr-e2e",
			wantSavePath:     "/downloads/sonarr-e2e",
			wantQbitCategory: "sonarr-e2e",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			infohash := uniqueInfohash(t)
			topicID := m.CreateTopic(t, magnet(infohash), clientID, tc.category)

			// Delivery: poll Marauder's status API until the scheduler pushes
			// the torrent (real tick, bounded at 135s).
			if !pollUntil(10*time.Second, 135*time.Second, func() bool {
				return m.StatusHasInfohash(t, topicID, infohash)
			}) {
				t.Fatalf("torrent %s never appeared in topic %s status", infohash, topicID)
			}

			// Category + save path: read back from qBittorrent directly.
			var info qbitTorrent
			if !pollUntil(2*time.Second, 15*time.Second, func() bool {
				var ok bool
				info, ok = q.TorrentInfo(t, infohash)
				return ok
			}) {
				t.Fatalf("torrent %s not found in qBittorrent", infohash)
			}
			if info.Category != tc.wantQbitCategory {
				t.Errorf("qBit category = %q, want %q", info.Category, tc.wantQbitCategory)
			}
			if info.SavePath != tc.wantSavePath {
				t.Errorf("qBit save_path = %q, want %q", info.SavePath, tc.wantSavePath)
			}
		})
	}
}
```

- [ ] **Step 2: Verify compile + gofmt; confirm it SKIPS without env**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go vet -tags=e2e ./e2e/... && gofmt -l e2e/ && go test -tags=e2e ./e2e/..."`
Expected: vet/gofmt clean; the test run prints `--- SKIP: TestDelivery` (because `MARAUDER_BASE_URL` is unset) and `ok` / `[no tests to run]` — proving the skip guard works.

- [ ] **Step 3: Commit**

```bash
git add backend/e2e/delivery_test.go
git commit -m "test(e2e): add table-driven delivery + qbit-category test"
```

---

### Task 5: Local live dry-run (behavioural green)

**Files:** none (verification task).

**Interfaces:**
- Consumes: the full harness (Tasks 1–4) + a running dev stack.

- [ ] **Step 1: Bring up the dev stack with qBittorrent**

```bash
cd deploy
cp .env.example .env
MASTER=$(openssl rand -base64 32); METRICS=$(openssl rand -hex 32)
sed -i "s|MARAUDER_MASTER_KEY=.*|MARAUDER_MASTER_KEY=$MASTER|" .env
sed -i "s|MARAUDER_METRICS_TOKEN=.*|MARAUDER_METRICS_TOKEN=$METRICS|" .env
docker compose --env-file .env -f docker-compose.yml -f docker-compose.dev.yml up -d --build
```

- [ ] **Step 2: Grab the qBittorrent temporary password**

Run: `docker logs deploy-qbittorrent-1 2>&1 | grep "temporary password" | awk '{print $NF}' | tail -1`
Expected: a short alphanumeric token. Save it as `QPW`.

- [ ] **Step 3: Run the harness in-network against the live stack**

```bash
docker run --rm --network deploy_default \
  -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend \
  -e MARAUDER_BASE_URL=http://gateway:6688 \
  -e MARAUDER_ADMIN_USER=admin -e MARAUDER_ADMIN_PASS=pleasechangeme \
  -e QBIT_URL=http://qbittorrent:6611 -e QBIT_PASSWORD="$QPW" \
  golang:1.25 go test -tags=e2e -count=1 -v ./e2e/...
```

Expected: `--- PASS: TestDelivery/category_sets_qbit_label_and_nests_save_path` then `PASS` / `ok`. The test logs show the torrent delivered and `category="sonarr-e2e"`, `save_path="/downloads/sonarr-e2e"`.

- [ ] **Step 4: Tear down**

```bash
cd deploy && docker compose -f docker-compose.yml -f docker-compose.dev.yml down -v
```

- [ ] **Step 5: No commit** (verification only). If the run failed, fix the harness in the relevant task before proceeding.

---

### Task 6: Swap CI to the Go harness + update docs

**Files:**
- Modify: `.github/workflows/e2e.yml` (replace the inline walkthrough run step)
- Modify: `docs/test-e2e-magnet.md` (pointer to the automated harness)

**Interfaces:**
- Consumes: the harness (Tasks 1–4); the existing `e2e.yml` stack-up + `steps.qbit.outputs.password` (with `::add-mask::`).

- [ ] **Step 1: Replace the inline walkthrough step in `.github/workflows/e2e.yml`**

Find the step named `Run the magnet -> qBittorrent walkthrough` (its `env: QBIT_PASSWORD:` and the `run: |` block containing steps `[1]`–`[6]`). Replace the **entire step** with:

```yaml
      - name: Run the Go e2e harness
        env:
          QBIT_PASSWORD: ${{ steps.qbit.outputs.password }}
        # Runs inside the compose network so the test reaches gateway:6688 and
        # qbittorrent:6611 by service DNS — avoids qBittorrent 5.x's host-port
        # 401. Network name `deploy_default` follows the compose project name
        # (this workflow runs compose from deploy/ with no -p); keep in sync.
        run: |
          docker run --rm --network deploy_default \
            -v "$PWD/backend:/backend" -w /backend \
            -e MARAUDER_BASE_URL=http://gateway:6688 \
            -e MARAUDER_ADMIN_USER=admin -e MARAUDER_ADMIN_PASS=pleasechangeme \
            -e QBIT_URL=http://qbittorrent:6611 -e QBIT_PASSWORD \
            golang:1.25 go test -tags=e2e -count=1 -v ./e2e/...
```

Leave the `Print stack logs on failure` and `Tear down` steps unchanged.

- [ ] **Step 2: Verify the workflow YAML is valid**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder:/repo" -w //repo node:20-alpine sh -c "npx --yes js-yaml .github/workflows/e2e.yml >/dev/null && echo YAML-OK"`
Expected: `YAML-OK` (parses cleanly).

- [ ] **Step 3: Add a pointer in `docs/test-e2e-magnet.md`**

Immediately after the title `# End-to-End Test: Magnet → qBittorrent` and its intro paragraph, insert:

```markdown
> **Automated version:** this walkthrough is automated as a Go harness in
> `backend/e2e/` (run with `go test -tags=e2e ./e2e/...`), exercised nightly by
> `.github/workflows/e2e.yml`. The manual steps below remain for humans who want
> to drive the flow by hand.
```

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/e2e.yml docs/test-e2e-magnet.md
git commit -m "test(e2e): run the Go harness in CI, retire inline bash walkthrough"
```

- [ ] **Step 5: Push and open the PR**

```bash
git push -u origin test/e2e-go-harness
gh pr create --base main --title "test(e2e): table-driven Go full-stack e2e harness" \
  --body "Replaces the inline-bash full-stack e2e walkthrough with a build-tagged Go harness in backend/e2e/ that drives the live stack over HTTP from inside the compose network. 1:1 migration of the existing delivery + qBit-category assertions; the table is built to grow. See docs/superpowers/specs/2026-06-22-e2e-go-harness-design.md."
```

(Confirm with the user before this push/PR step per their commit/PR policy.)

---

## Self-Review

**1. Spec coverage:**
- Build tag / invisible without tag → Tasks 1–4 (every `_test.go` has `//go:build e2e`; `doc.go` tagless; verified in Steps).
- Run inside compose network → Task 6 CI step + Task 5 dry-run.
- Env config + skip-when-unset → Task 1 `readEnv`; verified in Task 4 Step 2.
- Marauder API client → Task 2. qBit client → Task 3. Condition-based wait (replaces fixed sleep) → Task 1 `pollUntil`, used in Task 4.
- Delivery via Marauder + category via qBit + deterministic save path (`download_dir=/downloads`) → Tasks 2 & 4.
- No-synthetic-data infohash → Task 1 `uniqueInfohash`.
- CI swap + docs pointer → Task 6. Non-goals (RunFullPipeline, client-acceptance, deliver-now trigger, extra scenarios) → untouched.

**2. Placeholder scan:** No TBD/TODO; every code step shows full content; commands have expected output.

**3. Type consistency:** `config` fields used by `newMarauder`/`newQbit` match Task 1. `marauderClient.CreateQbitClient(t, downloadDir)`, `CreateTopic(t, url, clientID, category)`, `StatusHasInfohash(t, topicID, infohash)`, `qbitClient.TorrentInfo(t, infohash) (qbitTorrent, bool)`, and `qbitTorrent{Category,SavePath,State}` are referenced identically in Task 4. `pollUntil(interval, timeout, fn)` signature matches its two call sites.
