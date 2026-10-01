# Update Policy (Paused Adds, Only New Files) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Per-topic "Add updates paused" and "Download only new files" so a growing season pack does not re-download the episodes a user already moved off the disk (issue #205).

**Architecture:** Every `.torrent` delivery stores its file list in `topic_deliveries.files`. On an update of a single-release topic with "only new files" on, the scheduler adds the torrent paused, asks the client for the torrent's own file list, matches the previous version's files by relative path + size, marks them "do not download", and starts the torrent. Anything that prevents a safe selection leaves the torrent paused. A new client capability `registry.WithFileSelection` (qBittorrent, Transmission, Deluge) exposes list / skip / start; waiting and matching live once, in the scheduler.

**Tech Stack:** Go 1.26 (chi, pgx v5, goose migrations, zerolog, Prometheus), Postgres 17, React 19 + Vite + Vitest.

**Spec:** `docs/superpowers/specs/2026-09-30-update-file-selection-design.md`

## Global Constraints

- Settings default off: `add_paused_on_update BOOLEAN NOT NULL DEFAULT false`, `only_new_files BOOLEAN NOT NULL DEFAULT false`.
- An **update** is `t.LastHash != ""`. First delivery and first delivery after a reset start normally.
- Both settings are ignored for per-episode trackers (`isEpisodic(tr)`) and never reached for notify-only topics.
- "Same file" = same relative path (without the torrent's top folder) **and** same size.
- `only_new_files && replace_on_update && replace_delete_data` is rejected with 422 on both create and update (`topics.ErrOnlyNewFilesDeletesData`). On create, an omitted `replace_delete_data` defaults to `false` when `only_new_files` is on.
- File lists above `maxStoredFiles` (5000) are not stored.
- Never let an old file download when the selection cannot be proven: every failure after `Add` leaves the torrent paused. Failures after a successful `Add` never fail the check.
- Metric: `marauder_scheduler_file_selection_total{client,result}`, results `selected`, `no_new_files`, `paused_no_baseline`, `paused_magnet`, `paused_unreadable`, `unsupported`, `failed`.
- Go: 4-space-equivalent gofmt tabs, explicit types where the codebase uses them, comments explain *why*. Frontend: 2-space, `interface` for object shapes, `useT()` for every user-visible string (en + ru), `lucide-react` icons only.
- Commits: imperative, ≤72 chars, body for non-trivial changes, `Ref #205`. **No closing keywords. No AI/tool attribution, no `Co-Authored-By` trailer.**
- Backend commands run in Docker (never install Go locally). Frontend runs from the `marauder-fe-nm` volume (see CLAUDE.md "Common dev commands").

Backend test command used below (run from the worktree root in Git Bash):

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder-worktrees/feat-issue-205/backend:/backend" -w //backend golang:1.26 sh -c "go test -race ./internal/<pkg>/..."
```

## Review Focus

1. **Hybrid torrent with BEP 47 padding files** — padding entries must not count as files, so an update that only adds one episode still selects exactly one file. Pinned in Task 1 (`TestFiles_DropsPaddingFiles`).
2. **Uploader renamed the top folder between versions** (`Show S01E01-05` → `Show S01E01-06`) — old files must still be recognised. Pinned in Task 1 (`TestMatchesClientFile` "renamed root") and Task 7 (`TestRunCheck_OnlyNewFiles_RenamedRootStillSkipsOld`).
3. **Client lists fewer old files than expected** (a file renamed inside the torrent by the user, a partial client list) — must stay paused, not start with an old file wanted. Pinned in Task 7 (`TestRunCheck_OnlyNewFiles_PartialMatchStaysPaused`).
4. **qBittorrent add is asynchronous** — the first `Files` call returns nothing; the scheduler must poll, then succeed. Pinned in Task 7 (`TestRunCheck_OnlyNewFiles_PollsUntilClientListsFiles`) and Task 4 (404 → empty).
5. **Turning the setting on for an existing topic whose deliveries predate the migration** (`files` NULL everywhere) — must add paused with the "no earlier file list" note, never download everything. Pinned in Task 7 (`TestRunCheck_OnlyNewFiles_NoBaselineStaysPaused`) and Task 2 (`LatestFiles` skips NULL rows).

---

## File Structure

| File | Responsibility |
|---|---|
| `backend/internal/domain/domain.go` | + `TorrentFile`, `ClientFile`; `Topic.AddPausedOnUpdate/OnlyNewFiles`; `TopicDelivery.Files` |
| `backend/internal/torrentmeta/torrentmeta.go` (new) | read a `.torrent` file list; `SkipSet`; `MatchesClientFile`; `MatchSkip` |
| `backend/internal/torrentmeta/bencode.go` (new) | bounded bencode decoder |
| `backend/internal/torrentmeta/torrentmetatest/build.go` (new) | test-only `.torrent` builder shared by tests and the client check |
| `backend/internal/plugins/registry/registry.go` | + `WithFileSelection` |
| `backend/internal/db/migrations/0017_add_topic_update_policy.sql` (new) | columns |
| `backend/internal/db/repo/topics.go` | flags in columns/scan/create/update; `TopicFlags` |
| `backend/internal/db/repo/deliveries.go` | `files` on `Record`; `LatestFiles` |
| `backend/internal/topics/policy.go` (new) | `ErrOnlyNewFilesDeletesData`, `ValidUpdatePolicy` |
| `backend/internal/topics/create.go` | new inputs, default, validation |
| `backend/internal/api/handlers/topics.go` | request fields, validation, error mapping |
| `backend/internal/api/handlers/system.go` | `supports_file_selection` on each `clients` entry |
| `backend/internal/sonarr/poller.go` | carry the two flags through `Update` |
| `backend/internal/plugins/clients/{qbittorrent,transmission,deluge}/fileselection.go` (new) | `Files`/`SkipFiles`/`Start` |
| `backend/internal/plugins/clients/qbittorrent/qbittorrent.go` | send `stopped` with `paused` |
| `backend/internal/metrics/metrics.go` | `SchedulerFileSelectionTotal` |
| `backend/internal/scheduler/update_policy.go` (new) | plan, selection, notes |
| `backend/internal/scheduler/scheduler.go` | wiring through submit/send/record/notify |
| `backend/internal/plugins/clients/fileselectioncheck/fileselection_test.go` (new, build tag `clientcheck`) | real-client acceptance check |
| `frontend/src/components/topics/UpdatePolicyFields.tsx` (new) | the "when the topic updates" block |
| `frontend/src/components/topics/TopicForm.tsx`, `AddTopicCard.tsx`, `EditTopicCard.tsx`, `lib/api.ts`, `i18n/{en,ru}.ts` | wiring |
| `docs/update-policy.md` (new), `CHANGELOG.md`, `CLAUDE.md`, spec | docs |

---

### Task 1: Domain types, `torrentmeta` package, `WithFileSelection` capability

**Files:**
- Modify: `backend/internal/domain/domain.go` (after `AddOptions`, ~line 245; `Topic` ~line 104; `TopicDelivery` ~line 149)
- Modify: `backend/internal/plugins/registry/registry.go` (after `WithRemoval`, ~line 288)
- Create: `backend/internal/torrentmeta/bencode.go`, `backend/internal/torrentmeta/torrentmeta.go`
- Create: `backend/internal/torrentmeta/torrentmetatest/build.go`
- Test: `backend/internal/torrentmeta/torrentmeta_test.go`

**Interfaces:**
- Produces:
  - `domain.TorrentFile{Path string \`json:"path"\`; Size int64 \`json:"size"\`}`
  - `domain.ClientFile{Index int; Path string; Size int64; Wanted bool}`
  - `domain.Topic.AddPausedOnUpdate bool`, `domain.Topic.OnlyNewFiles bool`
  - `domain.TopicDelivery.Files []domain.TorrentFile`
  - `torrentmeta.Files(data []byte) ([]domain.TorrentFile, error)`, `torrentmeta.ErrV2Only`
  - `torrentmeta.SkipSet(prev, next []domain.TorrentFile) []domain.TorrentFile`
  - `torrentmeta.MatchesClientFile(c domain.ClientFile, f domain.TorrentFile) bool`
  - `torrentmeta.MatchSkip(client []domain.ClientFile, skip []domain.TorrentFile) (indices []int, matched int)`
  - `torrentmetatest.Torrent(name string, files []domain.TorrentFile) []byte`, `torrentmetatest.SingleFile(name string, size int64) []byte`
  - `registry.WithFileSelection` with `Files(ctx, rawConfig []byte, hash string) ([]domain.ClientFile, error)`, `SkipFiles(ctx, rawConfig []byte, hash string, indices []int) error`, `Start(ctx, rawConfig []byte, hash string) error`

- [ ] **Step 1: Add the domain types and fields**

In `domain.go`, add to `Topic` right after `NotifyOnlyAnnounceCurrent bool`:

```go
	// AddPausedOnUpdate adds every update of the topic to the client paused
	// (issue #205), so the user can pick files by hand. An update is a
	// delivery made while the topic already has a known release (LastHash is
	// set): the first delivery, and the first after a reset, start normally.
	AddPausedOnUpdate bool
	// OnlyNewFiles skips, on an update, every file the previous version of the
	// torrent already had (same relative path and size) and downloads only the
	// added ones (issue #205). When the scheduler cannot prove which files are
	// new it adds the torrent paused instead. Ignored for per-episode trackers.
	OnlyNewFiles bool
```

Add to `TopicDelivery` after `DeliveredAt time.Time`:

```go
	// Files is the delivered torrent's file list (issue #205), the baseline
	// the next update of the topic is compared with. Nil when unknown: a
	// magnet delivery, a delivery from before migration 0017, or a torrent
	// whose list could not be read or was too long to store.
	Files []TorrentFile
```

Add after `AddOptions`:

```go
// TorrentFile is one file of a .torrent's content as Marauder stores it per
// delivery (issue #205). Path is relative to the torrent's top folder and
// "/"-separated; Size is in bytes. Two files are "the same" when both match.
type TorrentFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// ClientFile is one file of a torrent as a download client lists it. Index is
// the client's own file id — the value it expects back when setting
// priorities, which is not always the position in the .torrent (clients hide
// BEP 47 padding files). Path is whatever the client reports, usually
// prefixed with the torrent's top folder. Wanted is false for a file marked
// "do not download".
type ClientFile struct {
	Index  int
	Path   string
	Size   int64
	Wanted bool
}
```

- [ ] **Step 2: Add the registry capability**

In `registry.go`, after `WithRemoval`:

```go
// WithFileSelection is an optional client capability (issue #205): list a
// torrent's files the way the client numbers them, mark some "do not
// download", and start a torrent that was added paused. It powers the
// per-topic "download only new files" policy. Files returns an empty slice,
// not an error, while the client does not know the torrent or its file list
// yet — a qBittorrent add is asynchronous — so the caller can poll. Matching
// files and deciding what to skip is the caller's job, so it exists once.
type WithFileSelection interface {
	Client
	Files(ctx context.Context, rawConfig []byte, hash string) ([]domain.ClientFile, error)
	SkipFiles(ctx context.Context, rawConfig []byte, hash string, indices []int) error
	Start(ctx context.Context, rawConfig []byte, hash string) error
}
```

- [ ] **Step 3: Write the test builder**

`backend/internal/torrentmeta/torrentmetatest/build.go`:

```go
// Package torrentmetatest builds minimal .torrent files for tests and for the
// real-client check. The piece hashes are zero bytes of the right length:
// clients accept such a torrent, and nothing ever downloads, because no peer
// has the data.
package torrentmetatest

import (
	"fmt"
	"strings"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

const pieceLength = 16384

// Torrent returns a multi-file .torrent named name. Each file's Path is split
// on "/" into the path list.
func Torrent(name string, files []domain.TorrentFile) []byte {
	var b strings.Builder
	var total int64
	b.WriteString("d4:infod5:filesl")
	for _, f := range files {
		total += f.Size
		fmt.Fprintf(&b, "d6:lengthi%de4:pathl", f.Size)
		for _, part := range strings.Split(f.Path, "/") {
			fmt.Fprintf(&b, "%d:%s", len(part), part)
		}
		b.WriteString("ee")
	}
	b.WriteString("e")
	writeTail(&b, name, total)
	return []byte(b.String())
}

// SingleFile returns a single-file .torrent.
func SingleFile(name string, size int64) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "d4:infod6:lengthi%de", size)
	writeTail(&b, name, size)
	return []byte(b.String())
}

func writeTail(b *strings.Builder, name string, total int64) {
	pieces := int((total + pieceLength - 1) / pieceLength)
	if pieces == 0 {
		pieces = 1
	}
	fmt.Fprintf(b, "4:name%d:%s12:piece lengthi%de6:pieces%d:%see",
		len(name), name, pieceLength, pieces*20, strings.Repeat("\x00", pieces*20))
}
```

- [ ] **Step 4: Write the failing tests**

`backend/internal/torrentmeta/torrentmeta_test.go`:

```go
package torrentmeta

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/torrentmeta/torrentmetatest"
)

func TestFiles_MultiFile(t *testing.T) {
	data := torrentmetatest.Torrent("Show S01", []domain.TorrentFile{
		{Path: "E01.mkv", Size: 100},
		{Path: "Subs/E01.srt", Size: 7},
	})
	got, err := Files(data)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	want := []domain.TorrentFile{{Path: "E01.mkv", Size: 100}, {Path: "Subs/E01.srt", Size: 7}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Files = %+v, want %+v", got, want)
	}
}

func TestFiles_SingleFile(t *testing.T) {
	got, err := Files(torrentmetatest.SingleFile("Movie.mkv", 4242))
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	want := []domain.TorrentFile{{Path: "Movie.mkv", Size: 4242}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Files = %+v, want %+v", got, want)
	}
}

// Hybrid (v1+v2) torrents list BEP 47 padding files in info.files. Clients
// hide them, so counting them would make every update look like it added files.
func TestFiles_DropsPaddingFiles(t *testing.T) {
	data := []byte("d4:infod5:filesl" +
		"d6:lengthi100e4:pathl7:E01.mkvee" +
		"d4:attr1:p6:lengthi28e4:pathl4:.pad2:28ee" +
		"d6:lengthi200e4:pathl4:.pad7:E02.mkvee" +
		"d6:lengthi300e4:pathl7:E03.mkvee" +
		"e4:name4:Show12:piece lengthi16384e6:pieces0:ee")
	got, err := Files(data)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	want := []domain.TorrentFile{{Path: "E01.mkv", Size: 100}, {Path: "E03.mkv", Size: 300}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Files = %+v, want %+v", got, want)
	}
}

func TestFiles_PrefersUTF8Path(t *testing.T) {
	data := []byte("d4:infod5:filesl" +
		"d6:lengthi5e4:pathl3:bade10:path.utf-8l4:goodee" +
		"e4:name1:x12:piece lengthi16384e6:pieces0:ee")
	got, err := Files(data)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if len(got) != 1 || got[0].Path != "good" {
		t.Errorf("Files = %+v, want path.utf-8 value", got)
	}
}

func TestFiles_V2OnlyIsReported(t *testing.T) {
	data := []byte("d4:infod9:file treed0:de4:name1:x12:piece lengthi16384eee")
	if _, err := Files(data); !errors.Is(err, ErrV2Only) {
		t.Errorf("err = %v, want ErrV2Only", err)
	}
}

func TestFiles_RejectsMalformedInput(t *testing.T) {
	cases := map[string][]byte{
		"empty":         nil,
		"not a dict":    []byte("i3e"),
		"no info":       []byte("d3:foo3:bare"),
		"truncated":     []byte("d4:infod5:filesl"),
		"negative size": []byte("d4:infod5:filesld6:lengthi-1e4:pathl1:aeeee4:name1:xee"),
		"deep nesting":  []byte(strings.Repeat("l", 100) + strings.Repeat("e", 100)),
		"huge string":   []byte("d4:info99999999999:x"),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Files(data); err == nil {
				t.Error("Files returned no error")
			}
		})
	}
}

func TestSkipSet(t *testing.T) {
	prev := []domain.TorrentFile{{Path: "E01.mkv", Size: 100}, {Path: "E02.mkv", Size: 200}}
	next := []domain.TorrentFile{
		{Path: "E01.mkv", Size: 100}, // unchanged -> skip
		{Path: "E02.mkv", Size: 201}, // re-encoded -> new
		{Path: "E03.mkv", Size: 300}, // added -> new
	}
	got := SkipSet(prev, next)
	want := []domain.TorrentFile{{Path: "E01.mkv", Size: 100}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SkipSet = %+v, want %+v", got, want)
	}
	if got := SkipSet(nil, next); len(got) != 0 {
		t.Errorf("SkipSet(nil, next) = %+v, want empty", got)
	}
}

func TestMatchesClientFile(t *testing.T) {
	f := domain.TorrentFile{Path: "Subs/E01.srt", Size: 7}
	cases := []struct {
		name string
		c    domain.ClientFile
		want bool
	}{
		{"with top folder", domain.ClientFile{Path: "Show S01/Subs/E01.srt", Size: 7}, true},
		{"renamed root", domain.ClientFile{Path: "Show S01E01-06/Subs/E01.srt", Size: 7}, true},
		{"no subfolder layout", domain.ClientFile{Path: "Subs/E01.srt", Size: 7}, true},
		{"windows separators", domain.ClientFile{Path: `Show S01\Subs\E01.srt`, Size: 7}, true},
		{"size differs", domain.ClientFile{Path: "Show S01/Subs/E01.srt", Size: 8}, false},
		{"deeper path", domain.ClientFile{Path: "Show/Extra/Subs/E01.srt", Size: 7}, false},
		{"not at a folder boundary", domain.ClientFile{Path: "Show/XSubs/E01.srt", Size: 7}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MatchesClientFile(tc.c, f); got != tc.want {
				t.Errorf("MatchesClientFile = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMatchSkip(t *testing.T) {
	client := []domain.ClientFile{
		{Index: 0, Path: "Show/E01.mkv", Size: 100},
		{Index: 2, Path: "Show/E02.mkv", Size: 200},
		{Index: 3, Path: "Show/E03.mkv", Size: 300},
	}
	skip := []domain.TorrentFile{{Path: "E02.mkv", Size: 200}, {Path: "E01.mkv", Size: 100}, {Path: "Gone.mkv", Size: 1}}
	indices, matched := MatchSkip(client, skip)
	if !reflect.DeepEqual(indices, []int{0, 2}) {
		t.Errorf("indices = %v, want [0 2] (client ids, sorted)", indices)
	}
	if matched != 2 {
		t.Errorf("matched = %d, want 2 (Gone.mkv has no client file)", matched)
	}
}
```

- [ ] **Step 5: Run the tests to see them fail**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder-worktrees/feat-issue-205/backend:/backend" -w //backend golang:1.26 sh -c "go test ./internal/torrentmeta/..."`
Expected: FAIL — `undefined: Files`, `undefined: SkipSet`, …

- [ ] **Step 6: Write the bencode decoder**

`backend/internal/torrentmeta/bencode.go`:

```go
package torrentmeta

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
)

// The input is a tracker response, so the decoder is bounded: nesting depth
// (the same bound infohash uses, turning a crafted payload's unbounded
// recursion into an error) and list length. Everything else is bounded by the
// input's own size, because every value consumes at least one byte.
const (
	maxDepth   = 32
	maxListLen = 100_000
)

var errUnexpectedEnd = errors.New("bencode: unexpected end of data")

type decoder struct {
	data []byte
	pos  int
}

// value decodes the value at d.pos. Integers become int64, strings string,
// lists []any and dictionaries map[string]any.
func (d *decoder) value(depth int) (any, error) {
	if depth > maxDepth {
		return nil, errors.New("bencode: nested too deeply")
	}
	if d.pos >= len(d.data) {
		return nil, errUnexpectedEnd
	}
	switch c := d.data[d.pos]; {
	case c == 'i':
		end := bytes.IndexByte(d.data[d.pos:], 'e')
		if end < 0 {
			return nil, errUnexpectedEnd
		}
		n, err := strconv.ParseInt(string(d.data[d.pos+1:d.pos+end]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("bencode: integer: %w", err)
		}
		d.pos += end + 1
		return n, nil
	case c == 'l':
		d.pos++
		out := []any{}
		for {
			if d.pos >= len(d.data) {
				return nil, errUnexpectedEnd
			}
			if d.data[d.pos] == 'e' {
				d.pos++
				return out, nil
			}
			if len(out) >= maxListLen {
				return nil, errors.New("bencode: list too long")
			}
			v, err := d.value(depth + 1)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
	case c == 'd':
		d.pos++
		out := map[string]any{}
		for {
			if d.pos >= len(d.data) {
				return nil, errUnexpectedEnd
			}
			if d.data[d.pos] == 'e' {
				d.pos++
				return out, nil
			}
			k, err := d.str()
			if err != nil {
				return nil, err
			}
			v, err := d.value(depth + 1)
			if err != nil {
				return nil, err
			}
			out[k] = v
		}
	case c >= '0' && c <= '9':
		return d.str()
	default:
		return nil, fmt.Errorf("bencode: unexpected byte %q at offset %d", c, d.pos)
	}
}

func (d *decoder) str() (string, error) {
	colon := bytes.IndexByte(d.data[d.pos:], ':')
	if colon < 0 {
		return "", errUnexpectedEnd
	}
	n, err := strconv.Atoi(string(d.data[d.pos : d.pos+colon]))
	if err != nil || n < 0 {
		return "", fmt.Errorf("bencode: bad string length at offset %d", d.pos)
	}
	start := d.pos + colon + 1
	if n > len(d.data)-start {
		return "", errUnexpectedEnd
	}
	d.pos = start + n
	return string(d.data[start:d.pos]), nil
}
```

- [ ] **Step 7: Write the package**

`backend/internal/torrentmeta/torrentmeta.go`:

```go
// Package torrentmeta reads the file list of a .torrent and decides which of
// its files an update already had (issue #205). It is separate from infohash,
// which only needs the raw span of the info dictionary: this package needs
// its decoded contents.
package torrentmeta

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// ErrV2Only reports a BitTorrent v2-only torrent: its files live in a
// "file tree" this package does not read. Callers treat it as "unknown".
var ErrV2Only = errors.New("torrent is v2-only (file tree without a v1 file list)")

// Files returns the content files of a .torrent. For a multi-file torrent the
// paths are relative to its top folder (info.name is left out, so a renamed
// top folder does not make every file look new); a single-file torrent is one
// entry named info.name. BEP 47 padding files are dropped because clients hide
// them.
func Files(data []byte) ([]domain.TorrentFile, error) {
	d := &decoder{data: data}
	root, err := d.value(0)
	if err != nil {
		return nil, err
	}
	top, ok := root.(map[string]any)
	if !ok {
		return nil, errors.New("torrent is not a bencoded dictionary")
	}
	info, ok := top["info"].(map[string]any)
	if !ok {
		return nil, errors.New("torrent has no info dictionary")
	}
	if list, ok := info["files"].([]any); ok {
		out := make([]domain.TorrentFile, 0, len(list))
		for i, raw := range list {
			entry, ok := raw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("files[%d] is not a dictionary", i)
			}
			parts := pathOf(entry)
			if len(parts) == 0 {
				return nil, fmt.Errorf("files[%d] has no path", i)
			}
			if isPadding(entry, parts) {
				continue
			}
			size, ok := entry["length"].(int64)
			if !ok || size < 0 {
				return nil, fmt.Errorf("files[%d] has no valid length", i)
			}
			out = append(out, domain.TorrentFile{Path: strings.Join(parts, "/"), Size: size})
		}
		return out, nil
	}
	if size, ok := info["length"].(int64); ok {
		name := firstString(info, "name.utf-8", "name")
		if name == "" || size < 0 {
			return nil, errors.New("single-file torrent has no name or a bad length")
		}
		return []domain.TorrentFile{{Path: name, Size: size}}, nil
	}
	if _, ok := info["file tree"]; ok {
		return nil, ErrV2Only
	}
	return nil, errors.New("torrent info has neither files nor length")
}

// pathOf prefers path.utf-8 (set by clients that also write a legacy-encoded
// path) over path.
func pathOf(entry map[string]any) []string {
	for _, key := range []string{"path.utf-8", "path"} {
		raw, ok := entry[key].([]any)
		if !ok || len(raw) == 0 {
			continue
		}
		parts := make([]string, 0, len(raw))
		for _, p := range raw {
			s, ok := p.(string)
			if !ok {
				parts = nil
				break
			}
			parts = append(parts, s)
		}
		if len(parts) > 0 {
			return parts
		}
	}
	return nil
}

func isPadding(entry map[string]any, parts []string) bool {
	if attr, ok := entry["attr"].(string); ok && strings.Contains(attr, "p") {
		return true
	}
	return parts[0] == ".pad"
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// SkipSet returns the entries of next that prev already had — same path and
// same size. A re-encoded episode keeps its name but changes size, so it is
// not in the set and downloads again.
func SkipSet(prev, next []domain.TorrentFile) []domain.TorrentFile {
	known := make(map[domain.TorrentFile]struct{}, len(prev))
	for _, f := range prev {
		known[f] = struct{}{}
	}
	var skip []domain.TorrentFile
	for _, f := range next {
		if _, ok := known[f]; ok {
			skip = append(skip, f)
		}
	}
	return skip
}

// MatchesClientFile reports whether a client's file is f. Clients prefix the
// path with the torrent's top folder, possibly renamed (qBittorrent content
// layouts), or leave it out; so the client path must equal f.Path either as a
// whole or after dropping exactly its first component. Sizes must be equal.
func MatchesClientFile(c domain.ClientFile, f domain.TorrentFile) bool {
	if c.Size != f.Size {
		return false
	}
	p := strings.ReplaceAll(c.Path, `\`, "/")
	if p == f.Path {
		return true
	}
	_, rest, ok := strings.Cut(p, "/")
	return ok && rest == f.Path
}

// MatchSkip maps skip onto the client's file list. It returns the sorted,
// de-duplicated client indices to mark "do not download", and how many skip
// entries matched at least one client file. A caller that gets matched <
// len(skip) must not start the torrent: an old file the client did not show
// would download again.
func MatchSkip(client []domain.ClientFile, skip []domain.TorrentFile) (indices []int, matched int) {
	bySize := make(map[int64][]domain.ClientFile, len(client))
	for _, c := range client {
		bySize[c.Size] = append(bySize[c.Size], c)
	}
	seen := make(map[int]bool, len(skip))
	for _, f := range skip {
		hit := false
		for _, c := range bySize[f.Size] {
			if !MatchesClientFile(c, f) {
				continue
			}
			hit = true
			if !seen[c.Index] {
				seen[c.Index] = true
				indices = append(indices, c.Index)
			}
		}
		if hit {
			matched++
		}
	}
	sort.Ints(indices)
	return indices, matched
}
```

- [ ] **Step 8: Run the tests to see them pass**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder-worktrees/feat-issue-205/backend:/backend" -w //backend golang:1.26 sh -c "go build ./... && go test -race ./internal/torrentmeta/..."`
Expected: PASS, build clean.

- [ ] **Step 9: Commit**

```bash
git add backend/internal/domain/domain.go backend/internal/plugins/registry/registry.go backend/internal/torrentmeta
git commit -m "feat(torrentmeta): read torrent file lists and match client files" -m "Adds the domain types, the WithFileSelection client capability and a
bounded bencode reader for the download-only-new-files policy.

Ref #205"
```

---

### Task 2: Migration and repositories

**Files:**
- Create: `backend/internal/db/migrations/0017_add_topic_update_policy.sql`
- Modify: `backend/internal/db/repo/topics.go` (`topicColumns` ~38, `scanTopic` ~46, `Create` ~83, `TopicFlags` ~542, `Update` ~573)
- Modify: `backend/internal/db/repo/deliveries.go`
- Test: `backend/internal/db/repo/topics_test.go`, `backend/internal/db/repo/deliveries_test.go`
- Create: `backend/internal/db/repo/update_policy_integration_test.go`

**Interfaces:**
- Consumes: `domain.TorrentFile`, `domain.Topic.AddPausedOnUpdate/OnlyNewFiles`, `domain.TopicDelivery.Files` (Task 1)
- Produces: `repo.TopicFlags.AddPausedOnUpdate bool`, `repo.TopicFlags.OnlyNewFiles bool`; `(*repo.Deliveries).LatestFiles(ctx context.Context, topicID uuid.UUID) ([]domain.TorrentFile, error)`; `Record` persists `Files`

- [ ] **Step 1: Write the migration**

`backend/internal/db/migrations/0017_add_topic_update_policy.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
-- Per-topic update policy (issue #205). add_paused_on_update adds every update
-- of the topic to the client paused; only_new_files skips the files the
-- previous version already had. Both default to false so existing topics keep
-- downloading exactly as before.
ALTER TABLE topics
    ADD COLUMN add_paused_on_update BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN only_new_files       BOOLEAN NOT NULL DEFAULT false;
-- The delivered torrent's file list, [{"path": "...", "size": N}, ...], with
-- paths relative to the torrent's top folder. NULL means unknown: a magnet
-- delivery, a row from before this migration, or a list too long to store.
ALTER TABLE topic_deliveries
    ADD COLUMN files JSONB;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE topic_deliveries
    DROP COLUMN files;
ALTER TABLE topics
    DROP COLUMN add_paused_on_update,
    DROP COLUMN only_new_files;
-- +goose StatementEnd
```

- [ ] **Step 2: Write the failing integration tests**

`backend/internal/db/repo/update_policy_integration_test.go`:

```go
//go:build integration

package repo

import (
	"context"
	"reflect"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// The two flags are DIFFERENT in each write so a transposed column or
// argument cannot pass unnoticed.
func TestTopicsUpdatePolicyRoundTrip(t *testing.T) {
	pool := integrationPool(t)
	topicsRepo := NewTopics(pool)
	userID := seedUser(t, pool)
	ctx := context.Background()

	topic := seedTopic(t, pool, userID, domain.TopicStatusActive, map[string]any{})
	if topic.AddPausedOnUpdate || topic.OnlyNewFiles {
		t.Fatalf("defaults = (%v, %v), want both false", topic.AddPausedOnUpdate, topic.OnlyNewFiles)
	}

	if _, err := topicsRepo.Update(ctx, topic.ID, userID, topic.DisplayName, nil, nil, "", "", nil,
		TopicFlags{AddPausedOnUpdate: true, OnlyNewFiles: false}, map[string]any{}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got := reload(t, pool, topic.ID)
	if !got.AddPausedOnUpdate {
		t.Error("stored AddPausedOnUpdate = false, want true")
	}
	if got.OnlyNewFiles {
		t.Error("stored OnlyNewFiles = true, want false")
	}

	if _, err := topicsRepo.Update(ctx, topic.ID, userID, topic.DisplayName, nil, nil, "", "", nil,
		TopicFlags{AddPausedOnUpdate: false, OnlyNewFiles: true}, map[string]any{}); err != nil {
		t.Fatalf("Update 2: %v", err)
	}
	got = reload(t, pool, topic.ID)
	if got.AddPausedOnUpdate || !got.OnlyNewFiles {
		t.Errorf("stored = (%v, %v), want (false, true)", got.AddPausedOnUpdate, got.OnlyNewFiles)
	}
}

func TestDeliveriesLatestFiles(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	userID := seedUser(t, pool)
	topic := seedTopic(t, pool, userID, domain.TopicStatusActive, map[string]any{})
	d := NewDeliveries(pool)

	got, err := d.LatestFiles(ctx, topic.ID)
	if err != nil || got != nil {
		t.Fatalf("LatestFiles on a topic without deliveries = (%v, %v), want (nil, nil)", got, err)
	}

	older := []domain.TorrentFile{{Path: "E01.mkv", Size: 100}}
	newer := []domain.TorrentFile{{Path: "E01.mkv", Size: 100}, {Path: "E02.mkv", Size: 200}}
	for _, rec := range []*domain.TopicDelivery{
		{TopicID: topic.ID, Infohash: "aaaa", Label: "v1", Files: older},
		{TopicID: topic.ID, Infohash: "bbbb", Label: "v2", Files: newer},
		// A later magnet delivery has no file list and must not hide v2's.
		{TopicID: topic.ID, Infohash: "cccc", Label: "v3"},
	} {
		if _, err := d.Record(ctx, rec); err != nil {
			t.Fatalf("Record %s: %v", rec.Label, err)
		}
	}
	// Pin the order explicitly: rows written in one test can share a timestamp.
	for hash, age := range map[string]string{"aaaa": "3 hours", "bbbb": "2 hours", "cccc": "1 hour"} {
		if _, err := pool.Exec(ctx,
			`UPDATE topic_deliveries SET delivered_at = now() - $2::interval WHERE topic_id = $1 AND infohash = $3`,
			topic.ID, age, hash); err != nil {
			t.Fatalf("age %s: %v", hash, err)
		}
	}

	got, err = d.LatestFiles(ctx, topic.ID)
	if err != nil {
		t.Fatalf("LatestFiles: %v", err)
	}
	if !reflect.DeepEqual(got, newer) {
		t.Errorf("LatestFiles = %+v, want the newest non-NULL list %+v", got, newer)
	}

	var files []byte
	if err := pool.QueryRow(ctx,
		`SELECT files FROM topic_deliveries WHERE topic_id = $1 AND infohash = 'cccc'`, topic.ID).Scan(&files); err != nil {
		t.Fatalf("read magnet row: %v", err)
	}
	if files != nil {
		t.Errorf("magnet delivery files = %s, want NULL", files)
	}
}
```

- [ ] **Step 3: Write the failing mock tests**

In `deliveries_test.go`, change `TestDeliveries_Record_InsertsNew`'s expectation to `WithArgs(topicID, "abc123", "s02e06", &clientID, nil)` and add:

```go
func TestDeliveries_Record_StoresFiles(t *testing.T) {
	repo, mock := newMockDeliveries(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	topicID := uuid.New()
	mock.ExpectExec(`INSERT INTO topic_deliveries \(topic_id, infohash, label, client_id, files\)`).
		WithArgs(topicID, "abc", "Show", (*uuid.UUID)(nil), []byte(`[{"path":"E01.mkv","size":100}]`)).
		WillReturnResult(pgconn.NewCommandTag("INSERT 0 1"))

	if _, err := repo.Record(context.Background(), &domain.TopicDelivery{
		TopicID: topicID, Infohash: "abc", Label: "Show",
		Files: []domain.TorrentFile{{Path: "E01.mkv", Size: 100}},
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
}

func TestDeliveries_LatestFiles_NoRowsIsNil(t *testing.T) {
	repo, mock := newMockDeliveries(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	topicID := uuid.New()
	mock.ExpectQuery(`SELECT files FROM topic_deliveries\s+WHERE topic_id = \$1 AND files IS NOT NULL\s+ORDER BY delivered_at DESC\s+LIMIT 1`).
		WithArgs(topicID).
		WillReturnRows(pgxmock.NewRows([]string{"files"}))

	got, err := repo.LatestFiles(context.Background(), topicID)
	if err != nil || got != nil {
		t.Errorf("LatestFiles = (%v, %v), want (nil, nil)", got, err)
	}
}
```

In `topics_test.go`: append `false, false, // add_paused_on_update, only_new_files` to `topicRow`'s returned slice and `"add_paused_on_update", "only_new_files"` to `topicColumnsAll`; update the "27 columns" comments to 29. In `TestTopics_Update_*` `WithArgs(...)` lists append `false, false` after `&interval` / the interval argument (they are `$14`, `$15`).

- [ ] **Step 4: Run to see the failures**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder-worktrees/feat-issue-205/backend:/backend" -w //backend golang:1.26 sh -c "go test ./internal/db/repo/..."`
Expected: FAIL — `LatestFiles undefined`, `unknown field AddPausedOnUpdate in struct literal of type TopicFlags`.

- [ ] **Step 5: Implement the topics repo changes**

In `topics.go`:
- `topicColumns`: change the last line to `replace_on_update, replace_delete_data, notify_only, notify_only_announce_current, add_paused_on_update, only_new_files\``.
- `scanTopic`: change the last Scan line to `&t.ReplaceOnUpdate, &t.ReplaceDeleteData, &t.NotifyOnly, &t.NotifyOnlyAnnounceCurrent, &t.AddPausedOnUpdate, &t.OnlyNewFiles,`.
- `Create`: column list gains `, add_paused_on_update, only_new_files` after `notify_only_announce_current`; `VALUES` gains `,$19,$20`; arguments gain `t.AddPausedOnUpdate, t.OnlyNewFiles,` after `t.NotifyOnly, t.NotifyOnlyAnnounceCurrent,`.
- `TopicFlags`: add after `NotifyOnlyAnnounceCurrent bool`:

```go
	// AddPausedOnUpdate / OnlyNewFiles are the update policy (issue #205).
	// See domain.Topic for the full semantics.
	AddPausedOnUpdate bool
	OnlyNewFiles      bool
```

- `Update`: insert `add_paused_on_update = $14, only_new_files = $15,` on its own line directly before `updated_at = now()` (after the `display_name_is_placeholder = CASE …` line, so the existing SQL regex in the mock test still matches), and append `flags.AddPausedOnUpdate, flags.OnlyNewFiles` after `checkIntervalSec` in the argument list.

- [ ] **Step 6: Implement the deliveries repo changes**

In `deliveries.go`, add `encoding/json` and `errors` imports, add `QueryRow(ctx context.Context, sql string, args ...any) pgx.Row` to `deliveriesPool`, and replace `Record` and add `LatestFiles`:

```go
// Record inserts a delivery, idempotently: re-detecting the same release
// (same topic + infohash) is a no-op rather than a duplicate row. Returns
// true when a new row was inserted, false when the delivery already
// existed. The label and file list are only set on first insert — an
// infohash's file list cannot change, so there is nothing to update.
func (r *Deliveries) Record(ctx context.Context, d *domain.TopicDelivery) (bool, error) {
	const q = `
INSERT INTO topic_deliveries (topic_id, infohash, label, client_id, files)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (topic_id, infohash) DO NOTHING`
	// A nil list is stored as NULL ("unknown"), which LatestFiles skips; an
	// empty list is a known torrent with no content files and is stored as [].
	var files any
	if d.Files != nil {
		raw, err := json.Marshal(d.Files)
		if err != nil {
			return false, fmt.Errorf("deliveries: marshal files: %w", err)
		}
		files = raw
	}
	ct, err := r.pool.Exec(ctx, q, d.TopicID, d.Infohash, d.Label, d.ClientID, files)
	if err != nil {
		return false, fmt.Errorf("deliveries: record: %w", err)
	}
	return ct.RowsAffected() > 0, nil
}

// LatestFiles returns the file list of the topic's newest delivery that has
// one — the baseline the download-only-new-files policy (issue #205) compares
// an update with. (nil, nil) means no delivery has a known list. Rows without
// a list (magnets, pre-0017 rows) are skipped rather than ending the search,
// so a magnet delivery in between does not erase the baseline.
func (r *Deliveries) LatestFiles(ctx context.Context, topicID uuid.UUID) ([]domain.TorrentFile, error) {
	const q = `
SELECT files FROM topic_deliveries
WHERE topic_id = $1 AND files IS NOT NULL
ORDER BY delivered_at DESC
LIMIT 1`
	var raw []byte
	err := r.pool.QueryRow(ctx, q, topicID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("deliveries: latest files: %w", err)
	}
	var files []domain.TorrentFile
	if err := json.Unmarshal(raw, &files); err != nil {
		return nil, fmt.Errorf("deliveries: decode files: %w", err)
	}
	if files == nil {
		files = []domain.TorrentFile{}
	}
	return files, nil
}
```

- [ ] **Step 7: Run unit and integration tests**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder-worktrees/feat-issue-205/backend:/backend" -w //backend golang:1.26 sh -c "go build ./... && go test -race ./internal/db/repo/..."`
Expected: PASS. Any remaining failure is a mock row or argument list in `topics_test.go` still missing the two new values — append `false, false` to it.

Then the integration suite (commands from CLAUDE.md, with this worktree's backend path):

```bash
docker network create marauder-itest-net 2>/dev/null; docker run --rm -d --name marauder-itest-pg --network marauder-itest-net -e POSTGRES_PASSWORD=test -e POSTGRES_DB=marauder_test postgres:17-alpine
docker run --rm --network marauder-itest-net -v "E:/Projects/Stukans/Marauder-worktrees/feat-issue-205/backend:/backend" -w //backend -e MARAUDER_TEST_DB_URL="postgres://postgres:test@marauder-itest-pg:5432/marauder_test?sslmode=disable" golang:1.26 sh -c "go test -tags=integration -race ./internal/db/repo/..."
```
Expected: PASS, including `TestTopicsUpdatePolicyRoundTrip` and `TestDeliveriesLatestFiles`.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/db
git commit -m "feat(db): store update policy flags and delivered file lists" -m "Migration 0017 adds add_paused_on_update and only_new_files to topics and
a files column to topic_deliveries; LatestFiles returns the newest known
file list as the baseline for the next update.

Ref #205"
```

---

### Task 3: Create/update API, validation, Sonarr, system info

**Files:**
- Create: `backend/internal/topics/policy.go`
- Modify: `backend/internal/topics/create.go` (`CreateInput` ~77, default block ~186, `domain.Topic` literal ~193)
- Modify: `backend/internal/api/handlers/topics.go` (`createTopicReq` ~111, `Create` ~180, `topicCreateProblem` ~236, `updateTopicReq` ~255, `Update` ~362)
- Modify: `backend/internal/api/handlers/system.go`
- Modify: `backend/internal/sonarr/poller.go:333`
- Test: `backend/internal/topics/policy_test.go`, `backend/internal/api/handlers/topics_handler_test.go`, `backend/internal/api/handlers/system_test.go` (new)

**Interfaces:**
- Consumes: `repo.TopicFlags.AddPausedOnUpdate/OnlyNewFiles` (Task 2), `registry.WithFileSelection` (Task 1)
- Produces: `topics.ErrOnlyNewFilesDeletesData`, `topics.ValidUpdatePolicy(replaceOnUpdate, replaceDeleteData, onlyNewFiles bool) error`; `topics.CreateInput.AddPausedOnUpdate/OnlyNewFiles bool`; JSON fields `add_paused_on_update`, `only_new_files` on POST/PUT; `/system/info` `clients[].supports_file_selection`

- [ ] **Step 1: Write the failing tests**

`backend/internal/topics/policy_test.go`:

```go
package topics

import (
	"errors"
	"testing"
)

func TestValidUpdatePolicy(t *testing.T) {
	cases := []struct {
		replace, deleteData, onlyNew bool
		wantErr                      bool
	}{
		{false, false, false, false},
		{true, true, false, false},  // replace + delete without only-new: fine
		{true, false, true, false},  // replace keeping data + only-new: fine
		{false, true, true, false},  // delete-data flag is inert while replace is off
		{true, true, true, true},    // the lossy mix
	}
	for _, tc := range cases {
		err := ValidUpdatePolicy(tc.replace, tc.deleteData, tc.onlyNew)
		if (err != nil) != tc.wantErr {
			t.Errorf("ValidUpdatePolicy(%v,%v,%v) = %v, wantErr %v", tc.replace, tc.deleteData, tc.onlyNew, err, tc.wantErr)
		}
		if err != nil && !errors.Is(err, ErrOnlyNewFilesDeletesData) {
			t.Errorf("err = %v, want ErrOnlyNewFilesDeletesData", err)
		}
	}
}
```

Add to `topics_handler_test.go` (it already has `boolPtr`, `authedReq`, `withURLParam`, `fakeTopicStore`, the `fake-create://` tracker):

```go
func TestTopicsUpdate_PassesUpdatePolicyFlags(t *testing.T) {
	store := &fakeTopicStore{getByID: &domain.Topic{ID: uuid.New(), TrackerName: fakeQualityTrackerName, DisplayName: "Show"}}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	body := updateTopicReq{DisplayName: "Show", AddPausedOnUpdate: boolPtr(true), OnlyNewFiles: boolPtr(false)}
	w := httptest.NewRecorder()
	h.Update(w, withURLParam(authedReq(t, uuid.New(), body), "id", uuid.New().String()))

	if w.Code != 200 {
		t.Fatalf("status %d, want 200; body %s", w.Code, w.Body.String())
	}
	if !store.lastFlags.AddPausedOnUpdate || store.lastFlags.OnlyNewFiles {
		t.Errorf("flags = %+v, want AddPausedOnUpdate only", store.lastFlags)
	}
}

func TestTopicsUpdate_OmittedUpdatePolicyFlags_PreserveExisting(t *testing.T) {
	store := &fakeTopicStore{getByID: &domain.Topic{
		ID: uuid.New(), TrackerName: fakeQualityTrackerName, DisplayName: "Show",
		AddPausedOnUpdate: true, OnlyNewFiles: true,
	}}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	w := httptest.NewRecorder()
	h.Update(w, withURLParam(authedReq(t, uuid.New(), updateTopicReq{DisplayName: "Show"}), "id", uuid.New().String()))

	if w.Code != 200 {
		t.Fatalf("status %d, want 200; body %s", w.Code, w.Body.String())
	}
	if !store.lastFlags.AddPausedOnUpdate || !store.lastFlags.OnlyNewFiles {
		t.Errorf("flags = %+v, want both preserved true", store.lastFlags)
	}
}

// Turning only_new_files on for a topic that already deletes the previous
// version's files must be refused: the old files would be deleted and never
// downloaded again.
func TestTopicsUpdate_OnlyNewFilesWithDeleteData_Rejected(t *testing.T) {
	store := &fakeTopicStore{getByID: &domain.Topic{
		ID: uuid.New(), TrackerName: fakeQualityTrackerName, DisplayName: "Show",
		ReplaceOnUpdate: true, ReplaceDeleteData: true,
	}}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	w := httptest.NewRecorder()
	h.Update(w, withURLParam(authedReq(t, uuid.New(), updateTopicReq{DisplayName: "Show", OnlyNewFiles: boolPtr(true)}), "id", uuid.New().String()))

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422; body %s", w.Code, w.Body.String())
	}
	if store.updateCalled {
		t.Error("store.Update was called for a rejected policy")
	}
}

func TestTopicsCreate_OnlyNewFiles_DefaultsToKeepData(t *testing.T) {
	store := &fakeTopicStore{}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	body := createTopicReq{URL: "fake-create://topic/only-new", ReplaceOnUpdate: true, OnlyNewFiles: true, AddPausedOnUpdate: true}
	w := httptest.NewRecorder()
	h.Create(w, authedReq(t, uuid.New(), body))

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	if store.created.ReplaceDeleteData {
		t.Error("omitted replace_delete_data must default to false with only_new_files")
	}
	if !store.created.OnlyNewFiles || !store.created.AddPausedOnUpdate {
		t.Errorf("created flags = (%v, %v), want both true", store.created.AddPausedOnUpdate, store.created.OnlyNewFiles)
	}
}

func TestTopicsCreate_OnlyNewFilesWithDeleteData_Rejected(t *testing.T) {
	store := &fakeTopicStore{}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	body := createTopicReq{URL: "fake-create://topic/lossy", ReplaceOnUpdate: true, ReplaceDeleteData: boolPtr(true), OnlyNewFiles: true}
	w := httptest.NewRecorder()
	h.Create(w, authedReq(t, uuid.New(), body))

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", w.Code, w.Body.String())
	}
	if store.created != nil {
		t.Error("store.Create was called for a rejected policy")
	}
}
```

`backend/internal/api/handlers/system_test.go`:

```go
package handlers

import (
	"context"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

type fakeInfoClient struct{ name string }

func (f *fakeInfoClient) Name() string                                  { return f.name }
func (f *fakeInfoClient) DisplayName() string                           { return f.name }
func (f *fakeInfoClient) ConfigSchema() map[string]any                  { return nil }
func (f *fakeInfoClient) Test(context.Context, []byte) error            { return nil }
func (f *fakeInfoClient) Add(context.Context, []byte, *domain.Payload, domain.AddOptions) error {
	return nil
}

type fakeSelectingClient struct{ fakeInfoClient }

func (f *fakeSelectingClient) Files(context.Context, []byte, string) ([]domain.ClientFile, error) {
	return nil, nil
}
func (f *fakeSelectingClient) SkipFiles(context.Context, []byte, string, []int) error { return nil }
func (f *fakeSelectingClient) Start(context.Context, []byte, string) error            { return nil }

func TestListClientInfos_SupportsFileSelection(t *testing.T) {
	infos := listClientInfos([]registry.Client{
		&fakeSelectingClient{fakeInfoClient{name: "selecting"}},
		&fakeInfoClient{name: "plain"},
	})
	if infos[0]["supports_file_selection"] != true {
		t.Errorf("selecting client: supports_file_selection = %v, want true", infos[0]["supports_file_selection"])
	}
	if infos[1]["supports_file_selection"] != false {
		t.Errorf("plain client: supports_file_selection = %v, want false", infos[1]["supports_file_selection"])
	}
	if infos[0]["name"] != "selecting" || infos[0]["display_name"] != "selecting" {
		t.Errorf("name fields = %v, want the plugin's names kept", infos[0])
	}
}
```

(`registry.Client` is exactly `Name`, `DisplayName`, `ConfigSchema`, `Test`, `Add`.)

- [ ] **Step 2: Run to see them fail**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder-worktrees/feat-issue-205/backend:/backend" -w //backend golang:1.26 sh -c "go test ./internal/topics/... ./internal/api/handlers/..."`
Expected: FAIL — `undefined: ValidUpdatePolicy`, `unknown field AddPausedOnUpdate in struct literal of type updateTopicReq`, `undefined: listClientInfos`.

- [ ] **Step 3: Implement the policy helper**

`backend/internal/topics/policy.go`:

```go
package topics

import "errors"

// ErrOnlyNewFilesDeletesData rejects a topic that both skips the previous
// version's files on an update and deletes those files when it replaces the
// previous version: the old episodes would be deleted and never downloaded
// again (issue #205).
var ErrOnlyNewFilesDeletesData = errors.New(
	"download only new files cannot be combined with deleting the previous version's files: the old files would be lost")

// ValidUpdatePolicy checks the combination of the replace-on-update (#101) and
// only-new-files (#205) settings a topic would store. replaceDeleteData only
// acts while replaceOnUpdate is on, so it is only a conflict then.
func ValidUpdatePolicy(replaceOnUpdate, replaceDeleteData, onlyNewFiles bool) error {
	if onlyNewFiles && replaceOnUpdate && replaceDeleteData {
		return ErrOnlyNewFilesDeletesData
	}
	return nil
}
```

- [ ] **Step 4: Wire create**

In `create.go` `CreateInput`, after `NotifyOnlyAnnounceCurrent bool`:

```go
	// AddPausedOnUpdate / OnlyNewFiles are the update policy (issue #205).
	AddPausedOnUpdate bool
	OnlyNewFiles      bool
```

Replace the replace-on-update default block with:

```go
	// Replace-on-update: keep the DB column's default (delete data = true) when
	// the caller doesn't specify — except with only-new-files, where deleting
	// the previous version's files would lose them (issue #205), so an omitted
	// flag defaults to keeping them. An explicit true is rejected below.
	replaceDeleteData := !in.OnlyNewFiles
	if in.ReplaceDeleteData != nil {
		replaceDeleteData = *in.ReplaceDeleteData
	}
	if err := ValidUpdatePolicy(in.ReplaceOnUpdate, replaceDeleteData, in.OnlyNewFiles); err != nil {
		return nil, err
	}
```

Add `AddPausedOnUpdate: in.AddPausedOnUpdate, OnlyNewFiles: in.OnlyNewFiles,` to the `domain.Topic` literal after `NotifyOnlyAnnounceCurrent`.

- [ ] **Step 5: Wire the handlers**

`createTopicReq`, after `NotifyOnlyAnnounceCurrent`:

```go
	// Update policy (issue #205). Plain bools: false is the right default.
	AddPausedOnUpdate bool `json:"add_paused_on_update,omitempty"`
	OnlyNewFiles      bool `json:"only_new_files,omitempty"`
```

In `Create`, pass `AddPausedOnUpdate: req.AddPausedOnUpdate, OnlyNewFiles: req.OnlyNewFiles,` to `topics.CreateInput`. In `topicCreateProblem`, add `errors.Is(err, topics.ErrOnlyNewFilesDeletesData)` to the `ErrQualityUnsupported` case (→ 422).

`updateTopicReq`, after `NotifyOnlyAnnounceCurrent`:

```go
	// Pointers so an omitted field preserves the topic's current value
	// (issue #205), matching the other flags.
	AddPausedOnUpdate *bool `json:"add_paused_on_update,omitempty"`
	OnlyNewFiles      *bool `json:"only_new_files,omitempty"`
```

In `Update`, after the `notifyOnlyAnnounceCurrent` block:

```go
	addPausedOnUpdate := existing.AddPausedOnUpdate
	if req.AddPausedOnUpdate != nil {
		addPausedOnUpdate = *req.AddPausedOnUpdate
	}
	onlyNewFiles := existing.OnlyNewFiles
	if req.OnlyNewFiles != nil {
		onlyNewFiles = *req.OnlyNewFiles
	}
	// Checked on the values the topic would store, so turning only-new-files
	// on for a topic that already deletes old files is refused too.
	if err := topics.ValidUpdatePolicy(replaceOnUpdate, replaceDeleteData, onlyNewFiles); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable(err.Error()))
		return
	}
```

(422, like the other semantic rejections here — an unsupported quality, an out-of-range interval — and like `POST /topics`.)

and add `AddPausedOnUpdate: addPausedOnUpdate, OnlyNewFiles: onlyNewFiles,` to the `repo.TopicFlags` literal.

- [ ] **Step 6: Sonarr and system info**

`poller.go:333` `repo.TopicFlags{…}`: add `AddPausedOnUpdate: existing.AddPausedOnUpdate, OnlyNewFiles: existing.OnlyNewFiles,`. In `poller_test.go` ~143 where the fake copies flags back, copy the two new fields too.

`system.go`: `"clients": listClientInfos(registry.ListClients()),` and add:

```go
// listClientInfos is like listPluginNames but adds the client capability the
// topic form needs to warn that a client cannot pause or select files
// (issue #205).
func listClientInfos(items []registry.Client) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, c := range items {
		_, selects := c.(registry.WithFileSelection)
		out = append(out, map[string]any{
			"name":                    c.Name(),
			"display_name":            c.DisplayName(),
			"supports_file_selection": selects,
		})
	}
	return out
}
```

- [ ] **Step 7: Run the tests**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder-worktrees/feat-issue-205/backend:/backend" -w //backend golang:1.26 sh -c "go build ./... && go vet ./... && go test -race ./internal/topics/... ./internal/api/... ./internal/sonarr/..."`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/topics backend/internal/api backend/internal/sonarr
git commit -m "feat(api): accept per-topic update policy on create and update" -m "Adds add_paused_on_update and only_new_files to POST/PUT /topics,
rejects only_new_files together with deleting the replaced version's
files, and reports supports_file_selection per client in /system/info.

Ref #205"
```

---

### Task 4: qBittorrent file selection

**Files:**
- Create: `backend/internal/plugins/clients/qbittorrent/fileselection.go`
- Modify: `backend/internal/plugins/clients/qbittorrent/qbittorrent.go:144-146` (paused field)
- Test: `backend/internal/plugins/clients/qbittorrent/fileselection_test.go`

**Interfaces:**
- Consumes: `registry.WithFileSelection`, `domain.ClientFile` (Task 1)
- Produces: `*qbittorrent.plugin` satisfies `registry.WithFileSelection`

- [ ] **Step 1: Write the failing tests**

`fileselection_test.go`:

```go
package qbittorrent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

var _ registry.WithFileSelection = (*plugin)(nil)

type selectServer struct {
	mu         sync.Mutex
	filesBody  string // "" => 404 (torrent not known yet)
	prioForm   map[string]string
	startCode  int // 0 => 200
	startCalls int
	resumeHash string
}

func (s *selectServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("Ok.")) })
	mux.HandleFunc("/api/v2/torrents/files", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.filesBody == "" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(s.filesBody))
	})
	mux.HandleFunc("/api/v2/torrents/filePrio", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		s.mu.Lock()
		s.prioForm = map[string]string{"hash": r.Form.Get("hash"), "id": r.Form.Get("id"), "priority": r.Form.Get("priority")}
		s.mu.Unlock()
	})
	mux.HandleFunc("/api/v2/torrents/start", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.startCalls++
		code := s.startCode
		s.mu.Unlock()
		if code != 0 {
			w.WriteHeader(code)
		}
	})
	mux.HandleFunc("/api/v2/torrents/resume", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		s.mu.Lock()
		s.resumeHash = r.Form.Get("hashes")
		s.mu.Unlock()
	})
	return mux
}

func selectCfg(url string) []byte {
	return []byte(`{"url":"` + url + `","username":"admin","password":"secret"}`)
}

func TestFiles_ReadsClientIndices(t *testing.T) {
	srv := &selectServer{filesBody: `[{"index":0,"name":"Show/E01.mkv","size":100,"priority":1},{"index":2,"name":"Show/E02.mkv","size":200,"priority":0}]`}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	got, err := newRemovePlugin().Files(context.Background(), selectCfg(ts.URL), "abc")
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	want := []domain.ClientFile{
		{Index: 0, Path: "Show/E01.mkv", Size: 100, Wanted: true},
		{Index: 2, Path: "Show/E02.mkv", Size: 200, Wanted: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Files = %+v, want %+v", got, want)
	}
}

// qBittorrent answers 404 until an asynchronous add has been processed; that
// is "not yet", not an error, so the scheduler keeps polling.
func TestFiles_UnknownTorrentIsEmpty(t *testing.T) {
	ts := httptest.NewServer((&selectServer{}).handler())
	defer ts.Close()

	got, err := newRemovePlugin().Files(context.Background(), selectCfg(ts.URL), "abc")
	if err != nil || len(got) != 0 {
		t.Errorf("Files = (%v, %v), want (empty, nil)", got, err)
	}
}

// Before qBittorrent 4.4 the file list has no index field; the position is the id.
func TestFiles_MissingIndexUsesPosition(t *testing.T) {
	srv := &selectServer{filesBody: `[{"name":"a","size":1,"priority":1},{"name":"b","size":2,"priority":1}]`}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	got, _ := newRemovePlugin().Files(context.Background(), selectCfg(ts.URL), "abc")
	if len(got) != 2 || got[1].Index != 1 {
		t.Errorf("Files = %+v, want position indices", got)
	}
}

func TestSkipFiles_SetsPriorityZero(t *testing.T) {
	srv := &selectServer{}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	if err := newRemovePlugin().SkipFiles(context.Background(), selectCfg(ts.URL), "abc", []int{0, 3}); err != nil {
		t.Fatalf("SkipFiles: %v", err)
	}
	want := map[string]string{"hash": "abc", "id": "0|3", "priority": "0"}
	if !reflect.DeepEqual(srv.prioForm, want) {
		t.Errorf("filePrio form = %v, want %v", srv.prioForm, want)
	}
}

func TestStart_FallsBackToResumeOn404(t *testing.T) {
	srv := &selectServer{startCode: http.StatusNotFound}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	if err := newRemovePlugin().Start(context.Background(), selectCfg(ts.URL), "abc"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if srv.resumeHash != "abc" {
		t.Errorf("resume hashes = %q, want abc (qBittorrent 4.x fallback)", srv.resumeHash)
	}
}

func TestStart_UsesStartOn5x(t *testing.T) {
	srv := &selectServer{}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	if err := newRemovePlugin().Start(context.Background(), selectCfg(ts.URL), "abc"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if srv.startCalls != 1 || srv.resumeHash != "" {
		t.Errorf("start calls = %d, resume = %q; want start only", srv.startCalls, srv.resumeHash)
	}
}
```

Add to `category_test.go` (it defines `captureAddFields`):

```go
// qBittorrent 5.0 renamed torrents/add's "paused" to "stopped". Both are sent
// so a paused add works on 4.x and 5.x; each version ignores the other name.
func TestAdd_PausedSendsStoppedToo(t *testing.T) {
	fields := captureAddFields(t, Config{Username: "admin", Password: "secret"},
		&domain.Payload{MagnetURI: "magnet:?xt=urn:btih:abc"}, domain.AddOptions{Paused: true})
	if fields["paused"] != "true" || fields["stopped"] != "true" {
		t.Errorf("paused=%q stopped=%q, want both \"true\"", fields["paused"], fields["stopped"])
	}
}

func TestAdd_NotPausedSendsNeither(t *testing.T) {
	fields := captureAddFields(t, Config{Username: "admin", Password: "secret"},
		&domain.Payload{MagnetURI: "magnet:?xt=urn:btih:abc"}, domain.AddOptions{})
	if _, ok := fields["paused"]; ok {
		t.Error("paused sent for a normal add")
	}
	if _, ok := fields["stopped"]; ok {
		t.Error("stopped sent for a normal add")
	}
}
```

- [ ] **Step 2: Run to see them fail**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder-worktrees/feat-issue-205/backend:/backend" -w //backend golang:1.26 sh -c "go test ./internal/plugins/clients/qbittorrent/..."`
Expected: FAIL — `*plugin does not implement registry.WithFileSelection (missing method Files)`.

- [ ] **Step 3: Implement**

In `qbittorrent.go` replace the paused block:

```go
	if opts.Paused {
		// qBittorrent 5.0 renamed this field to "stopped". Send both: each
		// version reads its own name and ignores the other.
		_ = mw.WriteField("paused", "true")
		_ = mw.WriteField("stopped", "true")
	}
```

`fileselection.go`:

```go
package qbittorrent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// Files implements registry.WithFileSelection. qBittorrent answers 404 for a
// torrent it has not finished adding (the add is asynchronous), which is
// reported as an empty list so the caller polls.
func (p *plugin) Files(ctx context.Context, rawConfig []byte, hash string) ([]domain.ClientFile, error) {
	var cfg Config
	if err := json.Unmarshal(rawConfig, &cfg); err != nil {
		return nil, fmt.Errorf("bad config: %w", err)
	}
	s, err := p.session(ctx, cfg)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(cfg.URL, "/")+"/api/v2/torrents/files?hash="+url.QueryEscape(hash), nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("qbit files status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var raw []struct {
		Index    *int   `json:"index"`
		Name     string `json:"name"`
		Size     int64  `json:"size"`
		Priority int    `json:"priority"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode files: %w", err)
	}
	out := make([]domain.ClientFile, 0, len(raw))
	for i, f := range raw {
		// index arrived in qBittorrent 4.4; before that the position is the id.
		idx := i
		if f.Index != nil {
			idx = *f.Index
		}
		out = append(out, domain.ClientFile{Index: idx, Path: f.Name, Size: f.Size, Wanted: f.Priority != 0})
	}
	return out, nil
}

// SkipFiles implements registry.WithFileSelection: priority 0 is "do not
// download".
func (p *plugin) SkipFiles(ctx context.Context, rawConfig []byte, hash string, indices []int) error {
	if len(indices) == 0 {
		return nil
	}
	ids := make([]string, len(indices))
	for i, idx := range indices {
		ids[i] = strconv.Itoa(idx)
	}
	return p.postForm(ctx, rawConfig, "/api/v2/torrents/filePrio", url.Values{
		"hash":     {hash},
		"id":       {strings.Join(ids, "|")},
		"priority": {"0"},
	})
}

// Start implements registry.WithFileSelection. qBittorrent 5.0 renamed
// torrents/resume to torrents/start; a 404 from start means an older server.
func (p *plugin) Start(ctx context.Context, rawConfig []byte, hash string) error {
	form := url.Values{"hashes": {hash}}
	err := p.postForm(ctx, rawConfig, "/api/v2/torrents/start", form)
	if statusErr, ok := err.(*qbitStatusError); ok && statusErr.code == http.StatusNotFound {
		return p.postForm(ctx, rawConfig, "/api/v2/torrents/resume", form)
	}
	return err
}

type qbitStatusError struct {
	path string
	code int
	body string
}

func (e *qbitStatusError) Error() string {
	return fmt.Sprintf("qbit %s status %d: %s", e.path, e.code, e.body)
}

func (p *plugin) postForm(ctx context.Context, rawConfig []byte, path string, form url.Values) error {
	var cfg Config
	if err := json.Unmarshal(rawConfig, &cfg); err != nil {
		return fmt.Errorf("bad config: %w", err)
	}
	s, err := p.session(ctx, cfg)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(cfg.URL, "/")+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("qbit %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return &qbitStatusError{path: path, code: resp.StatusCode, body: strings.TrimSpace(string(b))}
	}
	return nil
}
```

golangci-lint's `errorlint` rejects the type assertion on `err`; use `var statusErr *qbitStatusError; if errors.As(err, &statusErr) && statusErr.code == http.StatusNotFound {` instead (add `errors` import).

- [ ] **Step 4: Run the tests**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder-worktrees/feat-issue-205/backend:/backend" -w //backend golang:1.26 sh -c "go vet ./internal/plugins/clients/qbittorrent/ && go test -race ./internal/plugins/clients/qbittorrent/..."`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/plugins/clients/qbittorrent
git commit -m "feat(qbittorrent): list, skip and start torrent files" -m "Implements WithFileSelection and sends the qBittorrent 5 'stopped'
add field alongside 'paused'.

Ref #205"
```

---

### Task 5: Transmission file selection

**Files:**
- Create: `backend/internal/plugins/clients/transmission/fileselection.go`
- Test: `backend/internal/plugins/clients/transmission/fileselection_test.go`

**Interfaces:**
- Consumes: `registry.WithFileSelection`, `domain.ClientFile` (Task 1); the plugin's `do(ctx, c Config, method string, args map[string]any) (map[string]any, error)` (returns the whole response envelope)
- Produces: `*transmission.plugin` satisfies `registry.WithFileSelection`

- [ ] **Step 1: Write the failing tests**

```go
package transmission

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

var _ registry.WithFileSelection = (*plugin)(nil)

// newSelectServer answers the session-id dance, returns torrentsJSON for
// torrent-get, and records every call's method and arguments.
func newSelectServer(t *testing.T, torrentsJSON string) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var calls []map[string]any
	const sessionID = "sess-1"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") != sessionID {
			w.Header().Set("X-Transmission-Session-Id", sessionID)
			w.WriteHeader(http.StatusConflict)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		calls = append(calls, req)
		if req["method"] == "torrent-get" {
			w.Write([]byte(`{"result":"success","arguments":{"torrents":` + torrentsJSON + `}}`))
			return
		}
		w.Write([]byte(`{"result":"success","arguments":{}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestFiles_ReadsNamesAndWanted(t *testing.T) {
	srv, _ := newSelectServer(t, `[{"files":[{"name":"Show/E01.mkv","length":100},{"name":"Show/E02.mkv","length":200}],"fileStats":[{"wanted":true},{"wanted":false}]}]`)
	got, err := newPlugin().Files(context.Background(), []byte(`{"url":"`+srv.URL+`"}`), "abc")
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	want := []domain.ClientFile{
		{Index: 0, Path: "Show/E01.mkv", Size: 100, Wanted: true},
		{Index: 1, Path: "Show/E02.mkv", Size: 200, Wanted: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Files = %+v, want %+v", got, want)
	}
}

func TestFiles_UnknownTorrentIsEmpty(t *testing.T) {
	srv, _ := newSelectServer(t, `[]`)
	got, err := newPlugin().Files(context.Background(), []byte(`{"url":"`+srv.URL+`"}`), "abc")
	if err != nil || len(got) != 0 {
		t.Errorf("Files = (%v, %v), want (empty, nil)", got, err)
	}
}

func TestSkipFiles_SendsFilesUnwanted(t *testing.T) {
	srv, calls := newSelectServer(t, `[]`)
	if err := newPlugin().SkipFiles(context.Background(), []byte(`{"url":"`+srv.URL+`"}`), "abc", []int{0, 2}); err != nil {
		t.Fatalf("SkipFiles: %v", err)
	}
	last := (*calls)[len(*calls)-1]
	args := last["arguments"].(map[string]any)
	if last["method"] != "torrent-set" || !reflect.DeepEqual(args["files-unwanted"], []any{0.0, 2.0}) || !reflect.DeepEqual(args["ids"], []any{"abc"}) {
		t.Errorf("call = %v, want torrent-set ids [abc] files-unwanted [0 2]", last)
	}
}

func TestStart_SendsTorrentStart(t *testing.T) {
	srv, calls := newSelectServer(t, `[]`)
	if err := newPlugin().Start(context.Background(), []byte(`{"url":"`+srv.URL+`"}`), "abc"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	last := (*calls)[len(*calls)-1]
	if last["method"] != "torrent-start" {
		t.Errorf("method = %v, want torrent-start", last["method"])
	}
}
```

- [ ] **Step 2: Run to see them fail**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder-worktrees/feat-issue-205/backend:/backend" -w //backend golang:1.26 sh -c "go test ./internal/plugins/clients/transmission/..."`
Expected: FAIL — missing method `Files`.

- [ ] **Step 3: Implement**

`fileselection.go`:

```go
package transmission

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// Files implements registry.WithFileSelection. Transmission numbers files by
// their position in torrent-get's files array; a torrent it does not know
// yields an empty torrents array, reported as an empty list.
func (p *plugin) Files(ctx context.Context, rawConfig []byte, hash string) ([]domain.ClientFile, error) {
	var c Config
	if err := json.Unmarshal(rawConfig, &c); err != nil {
		return nil, fmt.Errorf("bad config: %w", err)
	}
	resp, err := p.do(ctx, c, "torrent-get", map[string]any{
		"ids":    []any{hash},
		"fields": []string{"files", "fileStats"},
	})
	if err != nil {
		return nil, err
	}
	if result, _ := resp["result"].(string); result != "success" {
		return nil, fmt.Errorf("transmission rejected torrent-get: %v", result)
	}
	// Re-decode through a typed struct rather than walking map[string]any.
	raw, _ := json.Marshal(resp["arguments"])
	var args struct {
		Torrents []struct {
			Files []struct {
				Name   string `json:"name"`
				Length int64  `json:"length"`
			} `json:"files"`
			FileStats []struct {
				Wanted bool `json:"wanted"`
			} `json:"fileStats"`
		} `json:"torrents"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode torrent-get: %w", err)
	}
	if len(args.Torrents) == 0 {
		return nil, nil
	}
	tor := args.Torrents[0]
	out := make([]domain.ClientFile, 0, len(tor.Files))
	for i, f := range tor.Files {
		wanted := true
		if i < len(tor.FileStats) {
			wanted = tor.FileStats[i].Wanted
		}
		out = append(out, domain.ClientFile{Index: i, Path: f.Name, Size: f.Length, Wanted: wanted})
	}
	return out, nil
}

// SkipFiles implements registry.WithFileSelection via torrent-set
// files-unwanted.
func (p *plugin) SkipFiles(ctx context.Context, rawConfig []byte, hash string, indices []int) error {
	if len(indices) == 0 {
		return nil
	}
	return p.simple(ctx, rawConfig, "torrent-set", map[string]any{"ids": []any{hash}, "files-unwanted": indices})
}

// Start implements registry.WithFileSelection.
func (p *plugin) Start(ctx context.Context, rawConfig []byte, hash string) error {
	return p.simple(ctx, rawConfig, "torrent-start", map[string]any{"ids": []any{hash}})
}

func (p *plugin) simple(ctx context.Context, rawConfig []byte, method string, args map[string]any) error {
	var c Config
	if err := json.Unmarshal(rawConfig, &c); err != nil {
		return fmt.Errorf("bad config: %w", err)
	}
	resp, err := p.do(ctx, c, method, args)
	if err != nil {
		return err
	}
	if result, _ := resp["result"].(string); result != "success" {
		return fmt.Errorf("transmission rejected %s: %v", method, result)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder-worktrees/feat-issue-205/backend:/backend" -w //backend golang:1.26 sh -c "go vet ./internal/plugins/clients/transmission/ && go test -race ./internal/plugins/clients/transmission/..."`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/plugins/clients/transmission
git commit -m "feat(transmission): list, skip and start torrent files" -m "Ref #205"
```

---

### Task 6: Deluge file selection

**Files:**
- Create: `backend/internal/plugins/clients/deluge/fileselection.go`
- Test: `backend/internal/plugins/clients/deluge/fileselection_test.go`

**Interfaces:**
- Consumes: `registry.WithFileSelection`, `domain.ClientFile`; the plugin's `session(ctx, c Config) (*session, error)` and `call(ctx, s *session, url, method string, params []any) (map[string]any, error)` (returns the envelope; the payload is `out["result"]`)
- Produces: `*deluge.plugin` satisfies `registry.WithFileSelection`

- [ ] **Step 1: Write the failing tests**

```go
package deluge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

var _ registry.WithFileSelection = (*plugin)(nil)

func newSelectServer(t *testing.T, statusJSON string) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var calls []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		calls = append(calls, req)
		switch req["method"] {
		case "auth.login":
			http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: "abc"})
			w.Write([]byte(`{"id":1,"result":true,"error":null}`))
		case "web.connected":
			w.Write([]byte(`{"id":2,"result":true,"error":null}`))
		case "core.get_torrent_status":
			w.Write([]byte(`{"id":3,"result":` + statusJSON + `,"error":null}`))
		default:
			w.Write([]byte(`{"id":4,"result":null,"error":null}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func delugeCfg(url string) []byte {
	b, _ := json.Marshal(Config{URL: url, Password: "secret"})
	return b
}

func TestFiles_ReadsFilesAndPriorities(t *testing.T) {
	srv, _ := newSelectServer(t, `{"files":[{"index":0,"path":"Show/E01.mkv","size":100},{"index":1,"path":"Show/E02.mkv","size":200}],"file_priorities":[4,0]}`)
	p := &plugin{sessions: map[string]*session{}}
	got, err := p.Files(context.Background(), delugeCfg(srv.URL), "abc")
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	want := []domain.ClientFile{
		{Index: 0, Path: "Show/E01.mkv", Size: 100, Wanted: true},
		{Index: 1, Path: "Show/E02.mkv", Size: 200, Wanted: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Files = %+v, want %+v", got, want)
	}
}

// Deluge answers an unknown torrent id with an empty status dictionary.
func TestFiles_UnknownTorrentIsEmpty(t *testing.T) {
	srv, _ := newSelectServer(t, `{}`)
	p := &plugin{sessions: map[string]*session{}}
	got, err := p.Files(context.Background(), delugeCfg(srv.URL), "abc")
	if err != nil || len(got) != 0 {
		t.Errorf("Files = (%v, %v), want (empty, nil)", got, err)
	}
}

// Deluge takes the whole priority list, so SkipFiles must keep the current
// priority of every file it does not skip.
func TestSkipFiles_KeepsOtherPriorities(t *testing.T) {
	srv, calls := newSelectServer(t, `{"files":[{"index":0,"path":"a","size":1},{"index":1,"path":"b","size":2},{"index":2,"path":"c","size":3}],"file_priorities":[4,7,4]}`)
	p := &plugin{sessions: map[string]*session{}}
	if err := p.SkipFiles(context.Background(), delugeCfg(srv.URL), "abc", []int{0, 2}); err != nil {
		t.Fatalf("SkipFiles: %v", err)
	}
	last := (*calls)[len(*calls)-1]
	params := last["params"].([]any)
	opts := params[1].(map[string]any)
	if last["method"] != "core.set_torrent_options" || !reflect.DeepEqual(params[0], []any{"abc"}) ||
		!reflect.DeepEqual(opts["file_priorities"], []any{0.0, 7.0, 0.0}) {
		t.Errorf("call = %v, want set_torrent_options([abc], {file_priorities: [0 7 0]})", last)
	}
}

func TestStart_ResumesTorrent(t *testing.T) {
	srv, calls := newSelectServer(t, `{}`)
	p := &plugin{sessions: map[string]*session{}}
	if err := p.Start(context.Background(), delugeCfg(srv.URL), "abc"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	last := (*calls)[len(*calls)-1]
	if last["method"] != "core.resume_torrent" || !reflect.DeepEqual(last["params"], []any{"abc"}) {
		t.Errorf("call = %v, want core.resume_torrent(abc)", last)
	}
}
```

- [ ] **Step 2: Run to see them fail**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder-worktrees/feat-issue-205/backend:/backend" -w //backend golang:1.26 sh -c "go test ./internal/plugins/clients/deluge/..."`
Expected: FAIL — missing method `Files`.

- [ ] **Step 3: Implement**

`fileselection.go`:

```go
package deluge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

type torrentFiles struct {
	Files []struct {
		Index int    `json:"index"`
		Path  string `json:"path"`
		Size  int64  `json:"size"`
	} `json:"files"`
	FilePriorities []int `json:"file_priorities"`
}

func (p *plugin) torrentFiles(ctx context.Context, rawConfig []byte, hash string) (*session, Config, *torrentFiles, error) {
	var c Config
	if err := json.Unmarshal(rawConfig, &c); err != nil {
		return nil, c, nil, fmt.Errorf("bad config: %w", err)
	}
	s, err := p.session(ctx, c)
	if err != nil {
		return nil, c, nil, err
	}
	out, err := p.call(ctx, s, c.URL, "core.get_torrent_status", []any{hash, []string{"files", "file_priorities"}})
	if err != nil {
		return nil, c, nil, err
	}
	raw, _ := json.Marshal(out["result"])
	var tf torrentFiles
	if err := json.Unmarshal(raw, &tf); err != nil {
		return nil, c, nil, fmt.Errorf("decode torrent status: %w", err)
	}
	return s, c, &tf, nil
}

// Files implements registry.WithFileSelection. Deluge answers an unknown
// torrent id with an empty status, reported as an empty list.
func (p *plugin) Files(ctx context.Context, rawConfig []byte, hash string) ([]domain.ClientFile, error) {
	_, _, tf, err := p.torrentFiles(ctx, rawConfig, hash)
	if err != nil {
		return nil, err
	}
	out := make([]domain.ClientFile, 0, len(tf.Files))
	for _, f := range tf.Files {
		wanted := true
		if f.Index < len(tf.FilePriorities) {
			wanted = tf.FilePriorities[f.Index] != 0
		}
		out = append(out, domain.ClientFile{Index: f.Index, Path: f.Path, Size: f.Size, Wanted: wanted})
	}
	return out, nil
}

// SkipFiles implements registry.WithFileSelection. Deluge sets priorities as
// one full list, so the current list is read first and only the skipped
// entries change; set_torrent_options is used because Deluge 2 dropped
// set_torrent_file_priorities.
func (p *plugin) SkipFiles(ctx context.Context, rawConfig []byte, hash string, indices []int) error {
	if len(indices) == 0 {
		return nil
	}
	s, c, tf, err := p.torrentFiles(ctx, rawConfig, hash)
	if err != nil {
		return err
	}
	prios := make([]int, len(tf.Files))
	for i := range prios {
		prios[i] = 4 // Deluge's "normal"
		if i < len(tf.FilePriorities) {
			prios[i] = tf.FilePriorities[i]
		}
	}
	for _, idx := range indices {
		if idx < 0 || idx >= len(prios) {
			return errors.New("deluge: file index out of range")
		}
		prios[idx] = 0
	}
	_, err = p.call(ctx, s, c.URL, "core.set_torrent_options", []any{[]string{hash}, map[string]any{"file_priorities": prios}})
	return err
}

// Start implements registry.WithFileSelection.
func (p *plugin) Start(ctx context.Context, rawConfig []byte, hash string) error {
	var c Config
	if err := json.Unmarshal(rawConfig, &c); err != nil {
		return fmt.Errorf("bad config: %w", err)
	}
	s, err := p.session(ctx, c)
	if err != nil {
		return err
	}
	_, err = p.call(ctx, s, c.URL, "core.resume_torrent", []any{hash})
	return err
}
```

- [ ] **Step 4: Run the tests**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder-worktrees/feat-issue-205/backend:/backend" -w //backend golang:1.26 sh -c "go vet ./internal/plugins/clients/deluge/ && go test -race ./internal/plugins/clients/deluge/..."`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/plugins/clients/deluge
git commit -m "feat(deluge): list, skip and start torrent files" -m "Ref #205"
```

---

### Task 7: Scheduler update policy

**Files:**
- Create: `backend/internal/scheduler/update_policy.go`
- Modify: `backend/internal/scheduler/scheduler.go` (`deliveriesRecorder` ~114; `runCheck` ~477, ~554, ~603; `notifyUpdated` ~622; `downloadAllPending` ~919; `submitToClient` ~1045; `sendViaClient` ~1068; `recordDelivery` ~1127)
- Modify: `backend/internal/metrics/metrics.go` (after `SchedulerReplacedPreviousTotal`)
- Test: `backend/internal/scheduler/update_policy_test.go` (new), `backend/internal/scheduler/scheduler_test.go` (fakes, `notifyUpdated` call sites ~2422/2443/2463)

**Interfaces:**
- Consumes: Task 1 (`torrentmeta.*`, `registry.WithFileSelection`, domain types), Task 2 (`LatestFiles`, `TopicDelivery.Files`)
- Produces: `metrics.SchedulerFileSelectionTotal`; `notifyUpdated(ctx, t, labels []string, note, authorComment string)`

- [ ] **Step 1: Extend the test fakes**

In `scheduler_test.go`:

`fakeDeliveries` gains `latestFiles []domain.TorrentFile` and `latestErr error` plus:

```go
func (f *fakeDeliveries) LatestFiles(_ context.Context, _ uuid.UUID) ([]domain.TorrentFile, error) {
	return f.latestFiles, f.latestErr
}
```

Add a selecting client fake (after `fakeClientPlugin`):

```go
// fakeSelectingClient adds registry.WithFileSelection to fakeClientPlugin.
// filesSeq is returned by successive Files calls (the last entry repeats), so
// a test can model qBittorrent's asynchronous add.
type fakeSelectingClient struct {
	fakeClientPlugin
	filesSeq    [][]domain.ClientFile
	filesCalls  int
	filesErr    error
	skipped     []int
	skipCalls   int
	skipErr     error
	startCalls  int
	startErr    error
	startedHash string
}

func (f *fakeSelectingClient) Files(_ context.Context, _ []byte, _ string) ([]domain.ClientFile, error) {
	f.filesCalls++
	if f.filesErr != nil {
		return nil, f.filesErr
	}
	i := f.filesCalls - 1
	if i >= len(f.filesSeq) {
		i = len(f.filesSeq) - 1
	}
	if i < 0 {
		return nil, nil
	}
	return f.filesSeq[i], nil
}

func (f *fakeSelectingClient) SkipFiles(_ context.Context, _ []byte, _ string, indices []int) error {
	f.skipCalls++
	f.skipped = indices
	return f.skipErr
}

func (f *fakeSelectingClient) Start(_ context.Context, _ []byte, hash string) error {
	f.startCalls++
	f.startedHash = hash
	return f.startErr
}
```

Update the three direct `notifyUpdated` calls (~2422, ~2443, ~2463) to pass `""` as the new 4th argument: `s.notifyUpdated(context.Background(), topic, []string{"s01e01"}, "", "")`.

- [ ] **Step 2: Write the failing tests**

`backend/internal/scheduler/update_policy_test.go`:

```go
package scheduler

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/events"
	"github.com/artyomsv/marauder/backend/internal/infohash"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/torrentmeta/torrentmetatest"
)

var (
	v1Files = []domain.TorrentFile{{Path: "E01.mkv", Size: 100}}
	v2Files = []domain.TorrentFile{{Path: "E01.mkv", Size: 100}, {Path: "E02.mkv", Size: 200}}
	// How a client lists v2: its own top folder, its own ids.
	v2Client = []domain.ClientFile{
		{Index: 0, Path: "Show S01/E01.mkv", Size: 100, Wanted: true},
		{Index: 1, Path: "Show S01/E02.mkv", Size: 200, Wanted: true},
	}
)

// torrentTracker is a single-release tracker whose update is the given .torrent.
func torrentTracker(data []byte) *fakeTracker {
	return &fakeTracker{
		name: "faketracker",
		checks: []checkResult{
			{check: &domain.Check{Hash: "new-hash", Extra: map[string]any{}}},
		},
		downloads: []downloadResult{{payload: &domain.Payload{TorrentFile: data, FileName: "x.torrent"}}},
	}
}

// selectingFixture swaps the fixture's client plugin for one with file selection.
func selectingFixture(t *testing.T, tr *fakeTracker) (*fixture, *fakeSelectingClient) {
	t.Helper()
	f := newFixture(t, tr)
	sel := &fakeSelectingClient{fakeClientPlugin: fakeClientPlugin{name: "fakeclient"}}
	f.s.lookupClient = func(string) registry.Client { return sel }
	return f, sel
}

func submittedBody(t *testing.T, f *fixture) string {
	t.Helper()
	for _, ev := range f.emitter.events {
		if ev.Type == events.DownloadSubmitted {
			return ev.Body
		}
	}
	t.Fatal("no download.submitted event")
	return ""
}

func TestRunCheck_FirstDelivery_NotPausedButFilesRecorded(t *testing.T) {
	data := torrentmetatest.Torrent("Show S01", v1Files)
	f, sel := selectingFixture(t, torrentTracker(data))
	f.topic.LastHash = ""
	f.topic.AddPausedOnUpdate = true
	f.topic.OnlyNewFiles = true

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if sel.lastOpts.Paused {
		t.Error("first delivery added paused, want normal start")
	}
	if sel.skipCalls != 0 || sel.startCalls != 0 {
		t.Errorf("selection ran on a first delivery (skip=%d start=%d)", sel.skipCalls, sel.startCalls)
	}
	if len(f.deliveries.recorded) != 1 || !reflect.DeepEqual(f.deliveries.recorded[0].Files, v1Files) {
		t.Errorf("recorded = %+v, want one delivery carrying v1's files", f.deliveries.recorded)
	}
}

func TestRunCheck_AddPausedOnUpdate_Only(t *testing.T) {
	f, sel := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.AddPausedOnUpdate = true

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if !sel.lastOpts.Paused {
		t.Error("update not added paused")
	}
	if sel.filesCalls != 0 || sel.startCalls != 0 {
		t.Error("add-paused alone must not touch file selection or start")
	}
	if body := submittedBody(t, f); !strings.Contains(body, "Added paused") {
		t.Errorf("body = %q, want the paused note", body)
	}
}

func TestRunCheck_OnlyNewFiles_SkipsOldAndStarts(t *testing.T) {
	data := torrentmetatest.Torrent("Show S01", v2Files)
	f, sel := selectingFixture(t, torrentTracker(data))
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = v1Files
	sel.filesSeq = [][]domain.ClientFile{v2Client}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if !sel.lastOpts.Paused {
		t.Error("only-new-files update must be added paused before selection")
	}
	if !reflect.DeepEqual(sel.skipped, []int{0}) {
		t.Errorf("skipped = %v, want [0] (E01)", sel.skipped)
	}
	wantHash, _ := infohash.FromTorrent(data)
	if sel.startCalls != 1 || sel.startedHash != wantHash {
		t.Errorf("start calls = %d hash = %q, want 1 call for %q", sel.startCalls, sel.startedHash, wantHash)
	}
	if body := submittedBody(t, f); !strings.Contains(body, "Downloading 1 new of 2 files") {
		t.Errorf("body = %q, want the selection note", body)
	}
	if rec := f.lastRecord(t); !rec.updated || rec.errMsg != "" {
		t.Errorf("record = %+v, want a clean updated check", rec)
	}
}

func TestRunCheck_OnlyNewFiles_WithAddPaused_DoesNotStart(t *testing.T) {
	f, sel := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.OnlyNewFiles = true
	f.topic.AddPausedOnUpdate = true
	f.deliveries.latestFiles = v1Files
	sel.filesSeq = [][]domain.ClientFile{v2Client}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if sel.skipCalls != 1 || sel.startCalls != 0 {
		t.Errorf("skip=%d start=%d, want skip once and no start", sel.skipCalls, sel.startCalls)
	}
}

func TestRunCheck_OnlyNewFiles_RenamedRootStillSkipsOld(t *testing.T) {
	f, sel := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01E01-02", v2Files)))
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = v1Files
	sel.filesSeq = [][]domain.ClientFile{{
		{Index: 0, Path: "Show S01E01-02/E01.mkv", Size: 100},
		{Index: 1, Path: "Show S01E01-02/E02.mkv", Size: 200},
	}}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if !reflect.DeepEqual(sel.skipped, []int{0}) || sel.startCalls != 1 {
		t.Errorf("skipped=%v start=%d, want [0] and started", sel.skipped, sel.startCalls)
	}
}

func TestRunCheck_OnlyNewFiles_PollsUntilClientListsFiles(t *testing.T) {
	orig := filesPollInterval
	filesPollInterval = time.Millisecond
	t.Cleanup(func() { filesPollInterval = orig })

	f, sel := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = v1Files
	sel.filesSeq = [][]domain.ClientFile{nil, nil, v2Client}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if sel.filesCalls != 3 || sel.startCalls != 1 {
		t.Errorf("files calls = %d start = %d, want 3 polls then a start", sel.filesCalls, sel.startCalls)
	}
}

func TestRunCheck_OnlyNewFiles_PartialMatchStaysPaused(t *testing.T) {
	f, sel := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = v1Files
	// The client shows E01 with another size: the old file cannot be found.
	sel.filesSeq = [][]domain.ClientFile{{
		{Index: 0, Path: "Show S01/E01.mkv", Size: 999},
		{Index: 1, Path: "Show S01/E02.mkv", Size: 200},
	}}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if sel.skipCalls != 0 || sel.startCalls != 0 {
		t.Errorf("skip=%d start=%d, want neither: the torrent must stay paused", sel.skipCalls, sel.startCalls)
	}
	if body := submittedBody(t, f); !strings.Contains(body, "Added paused") {
		t.Errorf("body = %q, want a paused note", body)
	}
	if rec := f.lastRecord(t); rec.errMsg != "" {
		t.Errorf("record errMsg = %q, want none: the delivery itself succeeded", rec.errMsg)
	}
}

func TestRunCheck_OnlyNewFiles_NoBaselineStaysPaused(t *testing.T) {
	f, sel := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.OnlyNewFiles = true

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if !sel.lastOpts.Paused || sel.filesCalls != 0 || sel.startCalls != 0 {
		t.Errorf("paused=%v files=%d start=%d, want paused and untouched", sel.lastOpts.Paused, sel.filesCalls, sel.startCalls)
	}
	if body := submittedBody(t, f); !strings.Contains(body, "no earlier file list") {
		t.Errorf("body = %q, want the no-baseline note", body)
	}
}

func TestRunCheck_OnlyNewFiles_MagnetStaysPaused(t *testing.T) {
	tr := singlePayloadTracker("c12fe1c06bba254a9dc9f519b335aa7c1367a88a")
	f, sel := selectingFixture(t, tr)
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = v1Files

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if !sel.lastOpts.Paused || sel.filesCalls != 0 {
		t.Errorf("paused=%v files=%d, want paused without selection", sel.lastOpts.Paused, sel.filesCalls)
	}
	if body := submittedBody(t, f); !strings.Contains(body, "magnet") {
		t.Errorf("body = %q, want the magnet note", body)
	}
}

func TestRunCheck_OnlyNewFiles_UnsupportedClientWarns(t *testing.T) {
	f := newFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = v1Files

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if f.clientPlugin.addCalls != 1 {
		t.Fatalf("add calls = %d, want 1", f.clientPlugin.addCalls)
	}
	if body := submittedBody(t, f); !strings.Contains(body, "cannot pause or select files") {
		t.Errorf("body = %q, want the unsupported note", body)
	}
}

func TestRunCheck_OnlyNewFiles_NoNewFilesStaysPaused(t *testing.T) {
	f, sel := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v1Files)))
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = v1Files

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if sel.filesCalls != 0 || sel.startCalls != 0 {
		t.Errorf("files=%d start=%d, want no client calls", sel.filesCalls, sel.startCalls)
	}
	if body := submittedBody(t, f); !strings.Contains(body, "no new files") {
		t.Errorf("body = %q, want the no-new-files note", body)
	}
}

func TestRunCheck_OnlyNewFiles_SkipErrorStaysPaused(t *testing.T) {
	f, sel := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = v1Files
	sel.filesSeq = [][]domain.ClientFile{v2Client}
	sel.skipErr = errors.New("boom")

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if sel.startCalls != 0 {
		t.Error("started after a failed skip")
	}
	if rec := f.lastRecord(t); !rec.updated || rec.errMsg != "" {
		t.Errorf("record = %+v, want a clean updated check", rec)
	}
}

func TestRunCheck_UpdatePolicy_IgnoredForEpisodicTracker(t *testing.T) {
	tr := torrentTracker(torrentmetatest.Torrent("Show S01", v2Files))
	tr.episodic = true
	f, sel := selectingFixture(t, tr)
	f.topic.OnlyNewFiles = true
	f.topic.AddPausedOnUpdate = true
	f.deliveries.latestFiles = v1Files

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if sel.lastOpts.Paused || sel.filesCalls != 0 {
		t.Errorf("paused=%v files=%d, want the plain delivery", sel.lastOpts.Paused, sel.filesCalls)
	}
}
```

(`fakeEmitter.events` and `fakeTracker.episodic` are the existing field names. The episodic test needs no trailing `ErrNoPendingEpisodes`: its check carries no `pending_episodes`, so the loop returns after the one delivery.)

- [ ] **Step 3: Run to see them fail**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder-worktrees/feat-issue-205/backend:/backend" -w //backend golang:1.26 sh -c "go test ./internal/scheduler/..."`
Expected: FAIL — `undefined: filesPollInterval`, `fakeDeliveries` does not satisfy `deliveriesRecorder` once the interface grows, etc.

- [ ] **Step 4: Add the metric**

In `metrics.go` after `SchedulerReplacedPreviousTotal`:

```go
	// SchedulerFileSelectionTotal counts outcomes of the per-topic "download
	// only new files" policy (issue #205) by client and result: selected,
	// no_new_files, paused_no_baseline, paused_magnet, paused_unreadable,
	// unsupported, failed. Every result but "selected" needs the user to act
	// in the client; "unsupported" means the whole torrent downloads.
	SchedulerFileSelectionTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "marauder_scheduler_file_selection_total",
			Help: "Outcomes of the download-only-new-files policy, partitioned by client and result.",
		},
		[]string{"client", "result"},
	)
```

- [ ] **Step 5: Write `update_policy.go`**

```go
package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/infohash"
	"github.com/artyomsv/marauder/backend/internal/metrics"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/torrentmeta"
)

// Update policy (issue #205): the per-topic "add updates paused" and
// "download only new files" settings. The rule that shapes everything here:
// once the torrent is in the client, no failure may let an old file download.
// Anything that cannot prove which files are new leaves the torrent paused
// for the user to finish by hand, and never fails the check — the delivery
// itself succeeded.

const (
	// maxStoredFiles caps the file list stored with a delivery. Above it the
	// list is not stored, and the topic's next update arrives paused.
	maxStoredFiles = 5000
	// fileSelectionTimeout bounds the wait for the client to list the new
	// torrent's files plus the skip and start calls.
	fileSelectionTimeout = 15 * time.Second
)

// filesPollInterval is how often the client is asked for a file list it does
// not have yet (a qBittorrent add is asynchronous). A var so tests can shorten it.
var filesPollInterval = 500 * time.Millisecond

// File-selection metric results (marauder_scheduler_file_selection_total).
const (
	selSelected         = "selected"
	selNoNewFiles       = "no_new_files"
	selPausedNoBaseline = "paused_no_baseline"
	selPausedMagnet     = "paused_magnet"
	selPausedUnreadable = "paused_unreadable"
	selUnsupported      = "unsupported"
	selFailed           = "failed"
)

// deliveryPlan says how one payload is handed to the client.
type deliveryPlan struct {
	paused bool
	// files is the payload's own file list, stored with the delivery so the
	// next update has a baseline. Nil when unknown.
	files []domain.TorrentFile
	// selection is set when only-new-files runs after Add.
	selection *fileSelection
	// fallbackResult/fallbackNote are set when only-new-files applies but
	// cannot run.
	fallbackResult string
	fallbackNote   string
}

type fileSelection struct {
	selector registry.WithFileSelection
	hash     string
	skip     []domain.TorrentFile
	total    int
}

// planDelivery decides the update policy for one payload. It runs before the
// pre-submit check-state guard so its database read does not widen the gap
// between that guard and Add.
func (s *Scheduler) planDelivery(ctx context.Context, log zerolog.Logger, t *domain.Topic, episodic bool, clientPlugin registry.Client, payload *domain.Payload) deliveryPlan {
	var plan deliveryPlan
	unreadable := false
	if payload.MagnetURI == "" && len(payload.TorrentFile) > 0 {
		files, err := torrentmeta.Files(payload.TorrentFile)
		switch {
		case err != nil:
			log.Debug().Err(err).Msg("could not read the torrent's file list")
			unreadable = true
		case len(files) > maxStoredFiles:
			log.Debug().Int("files", len(files)).Msg("torrent file list too long to store")
			unreadable = true
		default:
			plan.files = files
		}
	}
	// Per-episode trackers deliver one new episode per torrent already, and a
	// first delivery (or the first after a reset) has nothing to compare with.
	if episodic || t.LastHash == "" || (!t.AddPausedOnUpdate && !t.OnlyNewFiles) {
		return plan
	}
	plan.paused = true

	selector, canSelect := clientPlugin.(registry.WithFileSelection)
	if !canSelect {
		// Such a client ignores Paused too (µTorrent, downloadfolder); the
		// form says so, so this is only reached through the API or a changed
		// default client.
		log.Warn().Str("client", clientPlugin.Name()).
			Msg("update policy set but the client cannot pause or select files; adding normally")
		if t.OnlyNewFiles {
			plan.fallbackResult = selUnsupported
			plan.fallbackNote = "This client cannot pause or select files, so all files download."
		}
		return plan
	}
	if !t.OnlyNewFiles {
		return plan
	}
	switch {
	case payload.MagnetURI != "":
		plan.fallbackResult = selPausedMagnet
		plan.fallbackNote = "Added paused: a magnet link has no file list. Pick the new files in your client."
		return plan
	case unreadable || plan.files == nil:
		plan.fallbackResult = selPausedUnreadable
		plan.fallbackNote = "Added paused: could not read the torrent's file list. Pick the new files in your client."
		return plan
	}
	baseline := s.latestFiles(ctx, log, t.ID)
	if baseline == nil {
		plan.fallbackResult = selPausedNoBaseline
		plan.fallbackNote = "Added paused: no earlier file list to compare with. Pick the new files in your client."
		return plan
	}
	hash, err := infohash.FromTorrent(payload.TorrentFile)
	if err != nil {
		plan.fallbackResult = selPausedUnreadable
		plan.fallbackNote = "Added paused: could not read the torrent's file list. Pick the new files in your client."
		return plan
	}
	plan.selection = &fileSelection{
		selector: selector,
		hash:     hash,
		skip:     torrentmeta.SkipSet(baseline, plan.files),
		total:    len(plan.files),
	}
	return plan
}

// latestFiles loads the baseline. A read error degrades to "no baseline",
// which adds the torrent paused — never to downloading everything.
func (s *Scheduler) latestFiles(ctx context.Context, log zerolog.Logger, topicID uuid.UUID) []domain.TorrentFile {
	if s.deliveries == nil {
		return nil
	}
	files, err := s.deliveries.LatestFiles(ctx, topicID)
	if err != nil {
		log.Warn().Err(err).Msg("only-new-files: load previous file list failed")
		return nil
	}
	return files
}

// finishDelivery runs after a successful Add and returns the note for the
// download.submitted notification ("" for a plain delivery).
func (s *Scheduler) finishDelivery(ctx context.Context, log zerolog.Logger, t *domain.Topic, clientName string, rawConfig []byte, plan deliveryPlan) string {
	switch {
	case plan.selection != nil:
		return s.selectNewFiles(ctx, log, t, clientName, rawConfig, plan.selection)
	case plan.fallbackResult != "":
		metrics.SchedulerFileSelectionTotal.WithLabelValues(clientName, plan.fallbackResult).Inc()
		return plan.fallbackNote
	case plan.paused:
		return "Added paused."
	}
	return ""
}

// selectNewFiles skips the previous version's files in a torrent that was
// just added paused, then starts it unless the topic also asks for paused
// updates.
func (s *Scheduler) selectNewFiles(ctx context.Context, log zerolog.Logger, t *domain.Topic, clientName string, rawConfig []byte, sel *fileSelection) string {
	newCount := sel.total - len(sel.skip)
	if newCount == 0 {
		metrics.SchedulerFileSelectionTotal.WithLabelValues(clientName, selNoNewFiles).Inc()
		return "Added paused: this update has no new files."
	}
	ctx, cancel := context.WithTimeout(ctx, fileSelectionTimeout)
	defer cancel()
	fail := func(err error) string {
		log.Warn().Err(err).Msg("file selection failed; torrent left paused")
		metrics.SchedulerFileSelectionTotal.WithLabelValues(clientName, selFailed).Inc()
		return fmt.Sprintf("Added paused: file selection did not finish (%v). Check the torrent in your client.", err)
	}
	if len(sel.skip) > 0 {
		clientFiles, err := waitForFiles(ctx, rawConfig, sel)
		if err != nil {
			return fail(err)
		}
		indices, matched := torrentmeta.MatchSkip(clientFiles, sel.skip)
		if matched != len(sel.skip) {
			// Starting now would download an old file the client did not show.
			return fail(fmt.Errorf("the client listed %d of %d old files", matched, len(sel.skip)))
		}
		if err := sel.selector.SkipFiles(ctx, rawConfig, sel.hash, indices); err != nil {
			return fail(fmt.Errorf("skip files: %w", err))
		}
	}
	if t.AddPausedOnUpdate {
		metrics.SchedulerFileSelectionTotal.WithLabelValues(clientName, selSelected).Inc()
		return fmt.Sprintf("Added paused with %d new of %d files selected.", newCount, sel.total)
	}
	if err := sel.selector.Start(ctx, rawConfig, sel.hash); err != nil {
		return fail(fmt.Errorf("start: %w", err))
	}
	metrics.SchedulerFileSelectionTotal.WithLabelValues(clientName, selSelected).Inc()
	return fmt.Sprintf("Downloading %d new of %d files.", newCount, sel.total)
}

// waitForFiles polls the client until it lists the torrent's files.
func waitForFiles(ctx context.Context, rawConfig []byte, sel *fileSelection) ([]domain.ClientFile, error) {
	for {
		files, err := sel.selector.Files(ctx, rawConfig, sel.hash)
		if err != nil {
			return nil, fmt.Errorf("list files: %w", err)
		}
		if len(files) > 0 {
			return files, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("the client did not list the torrent's files in time: %w", ctx.Err())
		case <-time.After(filesPollInterval):
		}
	}
}
```

- [ ] **Step 6: Wire it into `scheduler.go`**

1. `deliveriesRecorder` gains `LatestFiles(ctx context.Context, topicID uuid.UUID) ([]domain.TorrentFile, error)`.
2. `runCheck`: next to `var delivered []string` (~477) add `var deliveryNote string`; at ~554 change to `delivered, deliveredHashes, deliveryNote, dlErr = s.downloadAllPending(ctx, log, t, tr, check, creds)`; at ~603 change to `s.notifyUpdated(ctx, t, delivered, deliveryNote, authorComment)`.
3. `notifyUpdated(ctx context.Context, t *domain.Topic, labels []string, note, authorComment string)`: after `body` is built and before `s.emit.Emit`, add:

```go
	// The update policy's outcome (issue #205) — the user may have to act in
	// the client, so it rides on the same notification.
	if note != "" {
		body += "\n" + note
	}
```

4. `downloadAllPending` returns `(delivered []string, deliveredHashes []string, note string, err error)`. At its top: `episodic := isEpisodic(tr)`. Change the submit call to `n, err := s.submitToClient(ctx, log, t, payload, label, episodic)` and, right after the error handling, `note = n`. Every `return delivered, deliveredHashes, X` becomes `return delivered, deliveredHashes, note, X`. Update its doc comment ("Returns (delivered, deliveredHashes, note, error) … note is the update-policy outcome of the last delivery, empty for plain deliveries").
5. `submitToClient(ctx, log, t, payload, label string, episodic bool) (string, error)`: pass `episodic` to both `sendViaClient` calls; the non-send error returns become `return "", …`.
6. `sendViaClient(ctx, log, cfg, t, payload, label string, episodic bool) (string, error)`: every early `return err` becomes `return "", err`. Directly after the decrypt succeeds and **before** the `VerifyCheckState` block, add `plan := s.planDelivery(ctx, log, t, episodic, clientPlugin, payload)`. Add `Paused: plan.paused,` to the `AddOptions` literal. Replace the tail with:

```go
	metrics.ClientSubmitTotal.WithLabelValues(cfg.ClientName, "ok").Inc()
	note := s.finishDelivery(ctx, log, t, cfg.ClientName, rawConfig, plan)
	s.recordDelivery(ctx, log, t, cfg, payload, label, plan.files)
	return note, nil
```

7. `recordDelivery(…, label string, files []domain.TorrentFile)`: add `Files: files,` to the `domain.TopicDelivery` literal and extend the doc comment: "files is the payload's file list (nil when unknown), the baseline for the next update's only-new-files selection (issue #205)."

- [ ] **Step 7: Run the tests**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder-worktrees/feat-issue-205/backend:/backend" -w //backend golang:1.26 sh -c "go build ./... && go vet ./... && go test -race ./internal/scheduler/..."`
Expected: PASS — new tests and every existing scheduler test.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/scheduler backend/internal/metrics
git commit -m "feat(scheduler): add updates paused or with only new files" -m "On an update of a single-release topic the scheduler now adds the
torrent paused when asked, and with only-new-files skips the files the
previous delivery already had before starting it. Any step that cannot
prove which files are new leaves the torrent paused and says so in the
download.submitted notification. Every .torrent delivery records its
file list as the next update's baseline.

Ref #205"
```

---

### Task 8: Frontend

**Files:**
- Create: `frontend/src/components/topics/UpdatePolicyFields.tsx`, `frontend/src/components/topics/UpdatePolicyFields.test.tsx`
- Modify: `frontend/src/components/topics/TopicForm.tsx` (types ~64-87, `ClientOption` ~36, state ~255, submit ~303, replace block ~508-536)
- Modify: `frontend/src/components/topics/AddTopicCard.tsx` (~31 initial values, ~87 payload), `EditTopicCard.tsx` (~33, ~58), `TopicFormPending.test.tsx` (~48 initial values)
- Modify: `frontend/src/lib/api.ts` (`UpdateTopicBody` ~392, `Topic` ~511, `SystemInfo` ~536)
- Modify: `frontend/src/i18n/en.ts` (~331), `frontend/src/i18n/ru.ts` (~329)

**Interfaces:**
- Consumes: JSON `add_paused_on_update`, `only_new_files`, topic fields `AddPausedOnUpdate`, `OnlyNewFiles`, `/system/info` `clients[].supports_file_selection` (Task 3)
- Produces: `UpdatePolicyFields` component; `TopicFormValues.addPausedOnUpdate/onlyNewFiles`

- [ ] **Step 1: Write the failing test**

`UpdatePolicyFields.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { UpdatePolicyFields, type UpdatePolicyValue } from "./UpdatePolicyFields";

const OFF: UpdatePolicyValue = {
  replaceOnUpdate: false,
  replaceDeleteData: true,
  addPausedOnUpdate: false,
  onlyNewFiles: false,
};

describe("UpdatePolicyFields", () => {
  it("reports the add-paused checkbox", async () => {
    const onChange = vi.fn();
    render(<UpdatePolicyFields value={OFF} onChange={onChange} episodic={false} unsupportedClient={null} />);
    await userEvent.click(screen.getByLabelText("Add updates paused"));
    expect(onChange).toHaveBeenCalledWith({ ...OFF, addPausedOnUpdate: true });
  });

  it("turns delete-files off when only-new-files is switched on", async () => {
    const onChange = vi.fn();
    const value = { ...OFF, replaceOnUpdate: true, replaceDeleteData: true };
    render(<UpdatePolicyFields value={value} onChange={onChange} episodic={false} unsupportedClient={null} />);
    await userEvent.click(screen.getByLabelText("Download only new files"));
    expect(onChange).toHaveBeenCalledWith({ ...value, onlyNewFiles: true, replaceDeleteData: false });
  });

  it("locks delete-files while only-new-files is on", () => {
    const value = { ...OFF, replaceOnUpdate: true, replaceDeleteData: false, onlyNewFiles: true };
    render(<UpdatePolicyFields value={value} onChange={() => {}} episodic={false} unsupportedClient={null} />);
    const del = screen.getByLabelText("Also delete the old files from disk") as HTMLInputElement;
    expect(del.disabled).toBe(true);
    expect(del.checked).toBe(false);
    expect(screen.getByText(/old files would be lost/i)).toBeInTheDocument();
  });

  it("hides the new settings for per-episode trackers", () => {
    render(<UpdatePolicyFields value={OFF} onChange={() => {}} episodic={true} unsupportedClient={null} />);
    expect(screen.queryByLabelText("Add updates paused")).toBeNull();
    expect(screen.queryByLabelText("Download only new files")).toBeNull();
    expect(screen.getByLabelText("Replace previous version on update")).toBeInTheDocument();
  });

  it("warns when the client cannot pause or select files", () => {
    const value = { ...OFF, onlyNewFiles: true };
    render(<UpdatePolicyFields value={value} onChange={() => {}} episodic={false} unsupportedClient="µTorrent box" />);
    expect(screen.getByText(/µTorrent box cannot pause or select files/)).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run to see it fail**

Run (from the worktree root; one-time volume setup is in CLAUDE.md):

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder-worktrees/feat-issue-205/frontend:/host:ro" -v marauder-fe-nm:/app -w //app node:22-alpine sh -c "cp -r /host/src /host/vitest.config.ts /host/vite.config.ts /host/index.html /host/tsconfig*.json /app/ 2>/dev/null; npx vitest run src/components/topics/UpdatePolicyFields.test.tsx"
```
Expected: FAIL — cannot resolve `./UpdatePolicyFields`.

- [ ] **Step 3: Add the strings**

`en.ts`, after `"topics.replaceOnUpdate.deleteData"`:

```ts
  "topics.updatePolicy.addPaused": "Add updates paused",
  "topics.updatePolicy.addPausedHelp":
    "When the torrent is updated, add the new version to the client paused so you can pick the files yourself. The first download starts as normal.",
  "topics.updatePolicy.onlyNewFiles": "Download only new files",
  "topics.updatePolicy.onlyNewFilesHelp":
    "When the torrent is updated, skip the files the previous version already had (same name and size) and download only the added ones. If Marauder cannot tell which files are new, it adds the torrent paused.",
  "topics.updatePolicy.deleteDataLocked":
    "Deleting old files is off: with “Download only new files” the old files would be lost.",
  "topics.updatePolicy.unsupported":
    "{client} cannot pause or select files. These settings need qBittorrent, Transmission or Deluge.",
```

`ru.ts`, same place:

```ts
  "topics.updatePolicy.addPaused": "Добавлять обновления на паузе",
  "topics.updatePolicy.addPausedHelp":
    "При обновлении торрента добавлять новую версию в клиент на паузе, чтобы вы сами выбрали файлы. Первая загрузка начинается как обычно.",
  "topics.updatePolicy.onlyNewFiles": "Скачивать только новые файлы",
  "topics.updatePolicy.onlyNewFilesHelp":
    "При обновлении торрента пропускать файлы, которые были в предыдущей версии (то же имя и размер), и скачивать только добавленные. Если Marauder не может определить новые файлы, торрент добавляется на паузе.",
  "topics.updatePolicy.deleteDataLocked":
    "Удаление старых файлов выключено: при «Скачивать только новые файлы» старые файлы были бы потеряны.",
  "topics.updatePolicy.unsupported":
    "{client} не умеет ставить на паузу и выбирать файлы. Эти настройки работают с qBittorrent, Transmission и Deluge.",
```

- [ ] **Step 4: Write the component**

`UpdatePolicyFields.tsx`:

```tsx
import { useT } from "@/i18n";

// The four "when the topic updates" settings. Grouped so TopicForm can hand
// them over as one value.
export interface UpdatePolicyValue {
  replaceOnUpdate: boolean;
  replaceDeleteData: boolean;
  addPausedOnUpdate: boolean;
  onlyNewFiles: boolean;
}

interface UpdatePolicyFieldsProps {
  value: UpdatePolicyValue;
  onChange: (next: UpdatePolicyValue) => void;
  // Per-episode trackers deliver one torrent per episode, so the paused and
  // only-new-files settings have nothing to act on (issue #205).
  episodic: boolean;
  // Display name of the receiving client when it cannot pause or select
  // files; null when it can or is not known.
  unsupportedClient: string | null;
}

// Replace-on-update (issue #101) and the update policy (issue #205). The two
// interact: with only-new-files on, deleting the replaced version's files
// would lose them for good, so that box is forced off and locked.
export function UpdatePolicyFields({
  value,
  onChange,
  episodic,
  unsupportedClient,
}: UpdatePolicyFieldsProps) {
  const t = useT();
  const set = (patch: Partial<UpdatePolicyValue>) => onChange({ ...value, ...patch });

  return (
    <div className="space-y-2 rounded-md border border-border/60 bg-muted/20 p-3">
      <label className="flex items-center gap-2 text-sm font-medium text-foreground">
        <input
          type="checkbox"
          checked={value.replaceOnUpdate}
          onChange={(e) => set({ replaceOnUpdate: e.target.checked })}
        />
        <span>{t("topics.replaceOnUpdate.label")}</span>
      </label>
      <p className="text-xs text-muted-foreground">{t("topics.replaceOnUpdate.help")}</p>
      {value.replaceOnUpdate && (
        <label className="flex items-center gap-2 pt-1 text-sm">
          <input
            type="checkbox"
            checked={value.replaceDeleteData && !value.onlyNewFiles}
            disabled={value.onlyNewFiles}
            onChange={(e) => set({ replaceDeleteData: e.target.checked })}
          />
          <span>{t("topics.replaceOnUpdate.deleteData")}</span>
        </label>
      )}
      {value.replaceOnUpdate && value.onlyNewFiles && (
        <p className="text-xs text-muted-foreground">{t("topics.updatePolicy.deleteDataLocked")}</p>
      )}

      {!episodic && (
        <>
          <label className="flex items-center gap-2 pt-2 text-sm font-medium text-foreground">
            <input
              type="checkbox"
              checked={value.addPausedOnUpdate}
              onChange={(e) => set({ addPausedOnUpdate: e.target.checked })}
            />
            <span>{t("topics.updatePolicy.addPaused")}</span>
          </label>
          <p className="text-xs text-muted-foreground">{t("topics.updatePolicy.addPausedHelp")}</p>

          <label className="flex items-center gap-2 pt-2 text-sm font-medium text-foreground">
            <input
              type="checkbox"
              checked={value.onlyNewFiles}
              onChange={(e) =>
                set(
                  e.target.checked
                    ? { onlyNewFiles: true, replaceDeleteData: false }
                    : { onlyNewFiles: false },
                )
              }
            />
            <span>{t("topics.updatePolicy.onlyNewFiles")}</span>
          </label>
          <p className="text-xs text-muted-foreground">{t("topics.updatePolicy.onlyNewFilesHelp")}</p>

          {unsupportedClient && (value.addPausedOnUpdate || value.onlyNewFiles) && (
            <p className="rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-700 dark:text-amber-400">
              {t("topics.updatePolicy.unsupported", { client: unsupportedClient })}
            </p>
          )}
        </>
      )}
    </div>
  );
}
```

- [ ] **Step 5: Wire it into the form, cards and types**

`lib/api.ts`:
- `UpdateTopicBody`: after `notify_only_announce_current?: boolean;` add `// Update policy (issue #205).` `add_paused_on_update?: boolean;` `only_new_files?: boolean;`.
- `Topic`: after `NotifyOnlyAnnounceCurrent: boolean;` add `AddPausedOnUpdate: boolean;` `OnlyNewFiles: boolean;`.
- `SystemInfo.clients`: `{ name: string; display_name: string; supports_file_selection: boolean }[];`.

`TopicForm.tsx`:
- Imports: add `import { useSystemInfo } from "@/lib/hooks/useSystemInfo";` and `import { UpdatePolicyFields } from "./UpdatePolicyFields";`.
- `ClientOption` gains `client_name: string;` (the `/clients` response already carries it).
- `TopicFormValues` gains, after `notifyOnlyAnnounceCurrent`: `// Update policy (issue #205).` `addPausedOnUpdate: boolean;` `onlyNewFiles: boolean;`.
- `delivery` initial state and `handleSubmit`'s `onSubmit({...})` both gain `addPausedOnUpdate` / `onlyNewFiles` (from `initial.*` and `delivery.*`).
- After `categorySuggestions` add:

```tsx
  // Name the receiving client when it cannot pause or select files, so the
  // update-policy settings can say they will not work there (issue #205).
  const systemInfo = useSystemInfo().data;
  const effectiveClient = clients.find((c) => c.id === effectiveClientId);
  const effectivePlugin = systemInfo?.clients?.find((p) => p.name === effectiveClient?.client_name);
  const unsupportedClient =
    effectiveClient && effectivePlugin && !effectivePlugin.supports_file_selection
      ? effectiveClient.display_name
      : null;
```

- Replace the whole replace-on-update `<div className="space-y-2 rounded-md …">…</div>` block inside `{!delivery.notifyOnly && (<>…</>)}` with:

```tsx
          <UpdatePolicyFields
            value={{
              replaceOnUpdate: delivery.replaceOnUpdate,
              replaceDeleteData: delivery.replaceDeleteData,
              addPausedOnUpdate: delivery.addPausedOnUpdate,
              onlyNewFiles: delivery.onlyNewFiles,
            }}
            onChange={(v) => setDelivery((d) => ({ ...d, ...v }))}
            episodic={!!match?.supports_episode_filter}
            unsupportedClient={unsupportedClient}
          />
```

`AddTopicCard.tsx`: initial values gain `addPausedOnUpdate: false, onlyNewFiles: false,`; the POST body gains `add_paused_on_update: v.addPausedOnUpdate, only_new_files: v.onlyNewFiles,`.
`EditTopicCard.tsx`: `initialFrom` gains `addPausedOnUpdate: topic.AddPausedOnUpdate ?? false, onlyNewFiles: topic.OnlyNewFiles ?? false,`; the PUT body gains the same two fields as the POST.
`TopicFormPending.test.tsx` (~48): its initial-values object gains `addPausedOnUpdate: false, onlyNewFiles: false,`.

- [ ] **Step 6: Run type check and all frontend tests**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder-worktrees/feat-issue-205/frontend:/host:ro" -v marauder-fe-nm:/app -w //app node:22-alpine sh -c "cp -r /host/src /host/vitest.config.ts /host/vite.config.ts /host/index.html /host/tsconfig*.json /app/ 2>/dev/null; npx tsc --noEmit && npx vitest run"
```
Expected: tsc clean; all tests PASS, including the 5 new ones. `wc -l frontend/src/components/topics/TopicForm.tsx` is below its old 565.

- [ ] **Step 7: Commit**

```bash
git add frontend/src
git commit -m "feat(frontend): topic settings for paused updates and new files" -m "Moves the replace-on-update block out of TopicForm into
UpdatePolicyFields and adds the two update-policy checkboxes, the locked
delete-files box and the unsupported-client note.

Ref #205"
```

---

### Task 9: Real-client check, docs, full verification

**Files:**
- Create: `backend/internal/plugins/clients/fileselectioncheck/fileselection_test.go`
- Create: `docs/update-policy.md`
- Modify: `CHANGELOG.md` (`[Unreleased]`), `CLAUDE.md`, `docs/superpowers/specs/2026-09-30-update-file-selection-design.md` (only if implementation diverged)

**Interfaces:**
- Consumes: the three plugins' `Add`, `Files`, `SkipFiles`, `Start`, `Remove`, `WithStatus`; `torrentmetatest.Torrent`; `torrentmeta.MatchSkip`

- [ ] **Step 1: Write the real-client check**

`fileselection_test.go` (build tag keeps it out of `go test ./...`):

```go
//go:build clientcheck

// Package fileselectioncheck runs the download-only-new-files steps against
// real torrent clients (issue #205): add a two-version torrent's second
// version paused, skip the first version's file, start it, and read the
// result back from the client. Point it at the clients from
// deploy/docker-compose.test-clients.yml:
//
//	MARAUDER_CHECK_QBIT_URLS="http://qbittorrent-5-2:6611,http://qbittorrent-5-1:6611"
//	MARAUDER_CHECK_QBIT_PASSWORD=...           (temporary password from the container log)
//	MARAUDER_CHECK_TRANSMISSION_URLS="http://transmission-4-1:9091/transmission/rpc"
//	MARAUDER_CHECK_DELUGE_URL="http://deluge:8112"  MARAUDER_CHECK_DELUGE_PASSWORD=deluge
//	go test -tags=clientcheck ./internal/plugins/clients/fileselectioncheck/ -v
//
// Every torrent is named TEST-205-<random> and removed with its data at the end.
package fileselectioncheck

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/infohash"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/torrentmeta"
	"github.com/artyomsv/marauder/backend/internal/torrentmeta/torrentmetatest"

	_ "github.com/artyomsv/marauder/backend/internal/plugins/clients/deluge"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/clients/qbittorrent"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/clients/transmission"
)

type target struct {
	plugin string
	config map[string]any
}

func targets() []target {
	var out []target
	for _, u := range split(os.Getenv("MARAUDER_CHECK_QBIT_URLS")) {
		out = append(out, target{"qbittorrent", map[string]any{"url": u, "username": "admin", "password": os.Getenv("MARAUDER_CHECK_QBIT_PASSWORD")}})
	}
	for _, u := range split(os.Getenv("MARAUDER_CHECK_TRANSMISSION_URLS")) {
		out = append(out, target{"transmission", map[string]any{"url": u}})
	}
	if u := os.Getenv("MARAUDER_CHECK_DELUGE_URL"); u != "" {
		out = append(out, target{"deluge", map[string]any{"url": u, "password": os.Getenv("MARAUDER_CHECK_DELUGE_PASSWORD")}})
	}
	return out
}

func split(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func TestSkipOldFilesOnRealClients(t *testing.T) {
	ts := targets()
	if len(ts) == 0 {
		t.Skip("no MARAUDER_CHECK_* client configured")
	}
	for _, tg := range ts {
		t.Run(tg.plugin+" "+tg.config["url"].(string), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			client := registry.GetClient(tg.plugin)
			sel, ok := client.(registry.WithFileSelection)
			if !ok {
				t.Fatalf("%s does not implement WithFileSelection", tg.plugin)
			}
			raw, _ := json.Marshal(tg.config)

			name := "TEST-205-" + uuid.NewString()[:8]
			v1 := []domain.TorrentFile{{Path: "E01.bin", Size: 1 << 20}}
			v2 := append(append([]domain.TorrentFile{}, v1...), domain.TorrentFile{Path: "E02.bin", Size: 2 << 20})
			data := torrentmetatest.Torrent(name, v2)
			hash, err := infohash.FromTorrent(data)
			if err != nil {
				t.Fatalf("infohash: %v", err)
			}
			t.Cleanup(func() {
				if rm, ok := client.(registry.WithRemoval); ok {
					_ = rm.Remove(context.Background(), raw, []string{hash}, true)
				}
			})

			if err := client.Add(ctx, raw, &domain.Payload{TorrentFile: data, FileName: name + ".torrent"}, domain.AddOptions{Paused: true}); err != nil {
				t.Fatalf("Add paused: %v", err)
			}
			if st, ok := client.(registry.WithStatus); ok {
				waitFor(t, ctx, func() bool {
					got, err := st.Status(ctx, raw, []string{hash})
					return err == nil && len(got) == 1 && got[0].State == registry.StateStopped
				}, "torrent to be listed as stopped (paused add honoured)")
			}

			var files []domain.ClientFile
			waitFor(t, ctx, func() bool {
				files, err = sel.Files(ctx, raw, hash)
				return err == nil && len(files) == len(v2)
			}, "client to list both files")

			indices, matched := torrentmeta.MatchSkip(files, torrentmeta.SkipSet(v1, v2))
			if matched != 1 {
				t.Fatalf("matched %d old files in %+v, want 1", matched, files)
			}
			if err := sel.SkipFiles(ctx, raw, hash, indices); err != nil {
				t.Fatalf("SkipFiles: %v", err)
			}
			if err := sel.Start(ctx, raw, hash); err != nil {
				t.Fatalf("Start: %v", err)
			}

			waitFor(t, ctx, func() bool {
				files, err = sel.Files(ctx, raw, hash)
				if err != nil {
					return false
				}
				for _, f := range files {
					isOld := strings.HasSuffix(f.Path, "E01.bin")
					if f.Wanted == isOld {
						return false
					}
				}
				return true
			}, "E01 skipped and E02 wanted")
			if st, ok := client.(registry.WithStatus); ok {
				waitFor(t, ctx, func() bool {
					got, err := st.Status(ctx, raw, []string{hash})
					return err == nil && len(got) == 1 && got[0].State != registry.StateStopped
				}, "torrent to leave the stopped state after Start")
			}
		})
	}
}

func waitFor(t *testing.T, ctx context.Context, cond func() bool, what string) {
	t.Helper()
	for !cond() {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", what)
		case <-time.After(500 * time.Millisecond):
		}
	}
}
```

(`registry.GetClient(name) Client` and `registry.TorrentStatus.State` exist; qBittorrent maps `pausedDL`/`stoppedDL` to `registry.StateStopped`.)

- [ ] **Step 2: Run it against the real clients**

```bash
docker compose -f deploy/docker-compose.test-clients.yml up -d --wait
docker network ls   # find the test-clients network name, e.g. deploy_default
docker compose -f deploy/docker-compose.test-clients.yml ps   # service names for the URLs
docker logs $(docker compose -f deploy/docker-compose.test-clients.yml ps -q <qbit-5.2-service>) 2>&1 | grep "temporary password"
```

Run the check inside that network (fill in the service names and passwords the commands above printed; the two qBittorrent containers each mint their own temporary password, so run one `go test` per qBittorrent if they differ):

```bash
docker run --rm --network <network> -v "E:/Projects/Stukans/Marauder-worktrees/feat-issue-205/backend:/backend" -w //backend \
  -e MARAUDER_CHECK_QBIT_URLS="http://<qbit-5.2-service>:<port>" -e MARAUDER_CHECK_QBIT_PASSWORD="<password>" \
  -e MARAUDER_CHECK_TRANSMISSION_URLS="http://<transmission-4.1-service>:9091/transmission/rpc" \
  -e MARAUDER_CHECK_DELUGE_URL="http://<deluge-service>:8112" -e MARAUDER_CHECK_DELUGE_PASSWORD="deluge" \
  golang:1.26 sh -c "go test -tags=clientcheck -v ./internal/plugins/clients/fileselectioncheck/"
```

Expected: every subtest PASS — qBittorrent 5.2.1, qBittorrent 5.1.4, Transmission 4.1.2, Deluge 2.2.0. If a client fails, fix its plugin (Tasks 4–6) with a new unit test that pins the real behaviour, then re-run. If qBittorrent's paused add is not honoured, that is the `paused`/`stopped` question from the spec. Stop the stack afterwards: `docker compose -f deploy/docker-compose.test-clients.yml down`.

- [ ] **Step 3: Write the docs**

`docs/update-policy.md` — a user guide with these sections, in plain words:
1. *What it is for* (growing season packs, limited disk).
2. *Add updates paused* — what happens, first download starts normally, reset counts as first.
3. *Download only new files* — "same file" means same name and size (top folder ignored), a re-encoded episode downloads again; a skipped file can still get a small partial piece on disk.
4. *When Marauder adds paused instead* — table: magnet link / no earlier file list (topics older than this version, first update after turning it on for an old topic) / unreadable torrent / client call failed / no new files — each with the notification text.
5. *With "Replace previous version"* — delete-files is locked off; replace removes the old torrent but keeps its files.
6. *Supported clients* — qBittorrent, Transmission, Deluge. Not µTorrent or the download folder.
7. *Not for per-episode trackers* (LostFilm).
8. *Metric* — `marauder_scheduler_file_selection_total{client,result}` with the result list.

`CHANGELOG.md` `[Unreleased]` → `### Added`:

```markdown
- Per-topic "Add updates paused" and "Download only new files" settings. When a
  torrent is updated (for example a season pack gains an episode), Marauder can
  add the new version paused, or skip the files the previous version already had
  and download only the new ones. Works with qBittorrent, Transmission and Deluge.
  See `docs/update-policy.md`. (#205)
```

`CLAUDE.md`:
- Backend table: new row `**torrentmeta**` — reads a `.torrent`'s file list (bounded bencode decoder, BEP 47 padding dropped, top folder left out), `SkipSet` (same path + size), `MatchesClientFile`/`MatchSkip` (client path equal, or equal after dropping its first component; a partial match must not start the torrent). `torrentmetatest` builds test torrents.
- `db / db/repo` row: `Topics` carries `add_paused_on_update`/`only_new_files` (migration `0017`); `Deliveries.Record` stores `files` JSONB, `LatestFiles` returns the newest non-NULL list.
- `plugins/registry` row: add `WithFileSelection` (`Files`/`SkipFiles`/`Start`; implemented by qBittorrent, Transmission, Deluge; `Files` empty = not yet known).
- `plugins/clients/<name>` row: which clients implement `WithFileSelection`; qBittorrent sends `stopped` with `paused`.
- Scheduler design: new paragraph **Update policy (issue #205)** — update = `LastHash != ""`; `planDelivery` runs before `VerifyCheckState`; fallbacks stay paused; notes ride on `download.submitted`; ignored for episodic; interaction with replace-on-update.
- `/system/info` mention: `clients[].supports_file_selection`.

Spec: amend only what the implementation changed. Known amendments to make now:
- §3.4 — `/system/info` extends each `clients` entry with `supports_file_selection` (no separate `client_plugins` list); on create an omitted `replace_delete_data` defaults to false with `only_new_files`; the rejection is 422 on create and update, not 400.
- §4.2 — the capability is `Files`/`SkipFiles(indices)`/`Start`; waiting and matching live in the scheduler; `ClientFile.Wanted` exists; Deluge uses `core.set_torrent_options`.
- §4.3 — result `unsupported` replaces `paused_unsupported` (such a client cannot pause either).

- [ ] **Step 4: Full verification**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder-worktrees/feat-issue-205/backend:/backend" -w //backend golang:1.26 sh -c "go build ./... && go vet ./... && test -z \"\$(gofmt -l .)\" && go test -race ./..."
docker run --rm -v "E:/Projects/Stukans/Marauder-worktrees/feat-issue-205/backend:/backend" -w //backend golangci/golangci-lint:latest golangci-lint run ./...
```

Plus the integration suite from Task 2 Step 7 and the frontend command from Task 8 Step 6. Expected: all green; `gofmt -l` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/plugins/clients/fileselectioncheck docs CHANGELOG.md CLAUDE.md
git commit -m "docs: document the per-topic update policy" -m "Adds the user guide, changelog entry, CLAUDE.md notes, the spec
amendments, and a build-tagged check that runs file selection against
real qBittorrent, Transmission and Deluge containers.

Ref #205"
```
