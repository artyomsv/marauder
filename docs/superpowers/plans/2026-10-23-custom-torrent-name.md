# Custom Torrent Folder Name Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A per-topic folder name (`default` / `custom` / `keep`) that Marauder applies to the torrent's top folder on delivery and keeps across updates (issue #225).

**Architecture:** Three new topic columns; validation in `internal/topics`; a new client capability `registry.WithRename` on qBittorrent, Transmission and Deluge; a scheduler step in `scheduler/folder_name.go` that plans before `VerifyCheckState` and renames/rechecks after `Add`, sharing one wait for the client's file list with the only-new-files selection; a `FolderNameFields` component in the topic form.

**Tech Stack:** Go 1.26, pgx + goose migrations, pgxmock, React 19 + Vitest.

**Spec:** `docs/superpowers/specs/2026-10-23-custom-torrent-name-design.md`

## Global Constraints

- **No local builds or tests.** Never run `go build/test/vet`, `golangci-lint`, `npm test`, `npx vitest`, `npx tsc` on the host. Allowed: `gofmt -l`/`gofmt -w <file>` (via Docker `golang:1.26` if no local gofmt), `prettier --write <file>`, git, grep. Tests are written first, then proven by CI on the pull request (`gh pr checks <n> --watch`, `gh run view <id> --log-failed`).
- Mode values exactly: `default`, `custom`, `keep`. JSON fields: `folder_name_mode`, `folder_name` (request); topic responses carry Go field names `FolderNameMode`, `FolderName`, `FolderNameSeen` (domain.Topic has no json tags).
- Name rules: trim spaces; 1..255 UTF-8 bytes; not `.`, `..`, `.pad`; no `/ \ < > : " | ? *` or control chars; no trailing `.` or space; not `CON PRN AUX NUL COM1–COM9 LPT1–LPT9` (case-insensitive, with or without extension).
- 422 when mode ≠ `default` AND `replace_on_update` AND `replace_delete_data`; on create an omitted `replace_delete_data` defaults to `false` when mode ≠ `default` (or only-new-files is on).
- Rename only a torrent with 0 bytes done. Any failed rename step leaves the torrent paused and never fails the check. Raw client errors go to logs only.
- Notes (exact copy): success `Saved in folder "<name>".`; cannot apply `Could not use folder name "<name>": <reason>. Saved in the tracker's folder.`; failure `Added paused: renaming the folder failed (<step>). Rename it in your client and start it.` Update-policy note first, folder note second, joined by one space.
- Metric `marauder_scheduler_folder_rename_total{client,result}`; results `renamed same_name magnet single_file no_root has_data unsupported failed`.
- Real-client torrents named `TEST-225-*`; Compose project `-p marauder225check`.
- Commit messages: imperative, ≤72 chars, `Ref #225` in the body, no AI attribution.
- Go: tabs, wrapped errors, table tests; TS: 2 spaces, `interface` for object shapes; React components ≤250 lines; `TopicForm.tsx` grows only by call sites.

## Review Focus

1. **Retry of a discarded tick** (same infohash delivered again, torrent already renamed and possibly running): expect `same_name`, no rename, no recheck, no "paused" claim — test in Task 7.
2. **Keep mode when the previous torrent is gone or on another client**: expect fallback to `folder_name_seen`, then to the tracker's name — test in Task 7.
3. **Folder name set while only-new-files runs**: the selection must use the file list read after the rename and still match — test in Task 7.
4. **PUT that omits the folder fields, and the Sonarr poller's update**: must leave the stored mode and name alone — tests in Tasks 1 and 3.
5. **Mode switched away from `keep`, and topic reset**: `folder_name_seen` cleared, so a later `keep` does not reuse a stale name — tests in Task 1.

---

### Task 1: Data layer — migration, domain, repo

**Files:**
- Create: `backend/internal/db/migrations/0018_add_topic_folder_name.sql`
- Modify: `backend/internal/domain/domain.go` (Topic, after `OnlyNewFiles`)
- Modify: `backend/internal/db/repo/topics.go` (`topicColumns`, `scanTopic`, `Create`, `Update`, `ResetCheckState`; new `SetFolderNameSeen`)
- Modify: `backend/internal/db/repo/deliveries.go` (new `LatestOther`)
- Modify: every caller of `Topics.Update` (`grep -rn "Topics.Update(\|\.Update(ctx, " backend/internal`) — handler and Sonarr poller
- Test: `backend/internal/db/repo/topics_test.go`, `deliveries_test.go` (pgxmock), `integration_test.go` (`//go:build integration`)

**Interfaces:**
- Produces:
  - `domain.FolderNameMode` string type with consts `FolderNameDefault = "default"`, `FolderNameCustom = "custom"`, `FolderNameKeep = "keep"`.
  - `domain.Topic.FolderNameMode FolderNameMode`, `FolderName string`, `FolderNameSeen string`.
  - `repo.FolderNamePolicy struct { Mode domain.FolderNameMode; Name string }`.
  - `(*Topics).Update(ctx, id, userID, displayName, clientID, notifierID, downloadDir, category, checkIntervalSec *int, flags TopicFlags, folder *FolderNamePolicy, extra)` — `folder == nil` leaves all three columns alone.
  - `(*Topics).SetFolderNameSeen(ctx context.Context, id uuid.UUID, name string) error`.
  - `(*Deliveries).LatestOther(ctx context.Context, topicID uuid.UUID, excludeInfohash string) (*domain.TopicDelivery, error)` — newest row with `infohash <> $2`; `(nil, nil)` when none.

- [ ] **Step 1: Migration.** Columns exactly as the spec's Data section (`folder_name_mode text NOT NULL DEFAULT 'default' CHECK (...)`, `folder_name text NULL`, `folder_name_seen text NULL`), goose Up/Down, comment block in the 0017 style.
- [ ] **Step 2: Failing tests.**
  - pgxmock `TestTopicsUpdate_NilFolderPolicy_LeavesColumns`: SQL contains `folder_name_mode = COALESCE($n::text, folder_name_mode)` and passes NULL args.
  - pgxmock `TestTopicsUpdate_FolderPolicy_ClearsSeenOnModeChange`: SQL contains `folder_name_seen = CASE WHEN $n::text IS NOT NULL AND $n::text <> folder_name_mode THEN NULL ELSE folder_name_seen END`.
  - pgxmock `TestTopicsResetCheckState_ClearsFolderNameSeen`: SQL contains `folder_name_seen = NULL`.
  - Integration `TestTopics_FolderName_RoundTrip`: create with `custom`/"My Show"; read back; `Update(..., nil, ...)` keeps it; `Update` to `keep` clears `folder_name` (stored NULL when mode ≠ custom) ; `SetFolderNameSeen("X")` then `Update` mode → `default` leaves `FolderNameSeen == ""`; `ResetCheckState` clears seen.
  - Integration `TestDeliveries_LatestOther_ExcludesOwnHash`: rows A (older), B (newer); `LatestOther(topic, B)` returns A; `LatestOther(topic, A)` returns B; empty table → `nil, nil`.
- [ ] **Step 3: Implement.** Add the three columns to `topicColumns`/`scanTopic`/`Create` (`folder_name` via `NULLIF($n,'')`). In `Update`, write `folder_name` as `CASE WHEN mode = 'custom' THEN name ELSE NULL END` when `folder != nil`. Update callers: the handler passes the policy it computes in Task 3 (for now `nil`), the Sonarr poller passes `nil`.
- [ ] **Step 4: gofmt** the changed Go files; expected no output from `gofmt -l`.
- [ ] **Step 5: Commit** `feat(topics): store per-topic folder name settings`.

### Task 2: Validation in `internal/topics`

**Files:**
- Create: `backend/internal/topics/foldername.go`, `foldername_test.go`
- Modify: `backend/internal/topics/policy.go`, `policy_test.go`, `create.go` (CreateInput + defaults)

**Interfaces:**
- Consumes: `domain.FolderNameMode` (Task 1).
- Produces:
  - `var ErrFolderNameInvalid` (message names the broken rule via wrapping: `fmt.Errorf("%w: <rule>", ErrFolderNameInvalid)`), `var ErrFolderNameDeletesData`.
  - `func NormalizeFolderName(mode domain.FolderNameMode, name string) (string, error)` — validates the mode, trims, returns `""` for non-custom modes, applies the name rules for `custom`.
  - `func ValidUpdatePolicy(replaceOnUpdate, replaceDeleteData, onlyNewFiles bool, folderMode domain.FolderNameMode) error` — signature extended; returns `ErrFolderNameDeletesData` for the new conflict.
  - `CreateInput.FolderNameMode domain.FolderNameMode`, `CreateInput.FolderName string`.

- [ ] **Step 1: Failing table tests** `TestNormalizeFolderName` — accept: `"My Show"`, `"  Show  "`→`"Show"`, 255-byte ASCII, `"Сериал"`, `"Console"` — reject: `""` (custom), 256 bytes, `"."`, `".."`, `".pad"`, `"a/b"`, `"a\\b"`, `"a:b"`, `"a*"`, `"tab\t"`, `"name."`, `"CON"`, `"nul.mkv"`, `"Com3"`, `"LPT9.txt"`; mode `"bogus"` → error; mode `default`/`keep` with any name → `""`, nil. `TestValidUpdatePolicy` gains rows for each folder mode × replace × deleteData.
- [ ] **Step 2: Failing test** `TestBuildAndCreate_FolderName_DefaultsDeleteDataOff`: `CreateInput{ReplaceOnUpdate: true, FolderNameMode: "custom", FolderName: "X"}` stores `ReplaceDeleteData == false`; explicit `true` → `ErrFolderNameDeletesData`; invalid name → `ErrFolderNameInvalid`.
- [ ] **Step 3: Implement.** In `create.go`: `replaceDeleteData := !in.OnlyNewFiles && in.FolderNameMode is default/empty`; empty mode means `default`.
- [ ] **Step 4: gofmt.**
- [ ] **Step 5: Commit** `feat(topics): validate folder names and refuse delete-data`.

### Task 3: API — create and update

**Files:**
- Modify: `backend/internal/api/handlers/topics.go` (`createTopicReq`, `updateTopicReq`, Create, Update, `topicCreateProblem`)
- Test: `backend/internal/api/handlers/topics_test.go` (or the existing update-policy handler test file)

**Interfaces:**
- Consumes: Task 1 `repo.FolderNamePolicy`, Task 2 `NormalizeFolderName`, `ValidUpdatePolicy`, errors.
- Produces: request fields `folder_name_mode *string`, `folder_name *string` on update; plain `string` on create.

- [ ] **Step 1: Failing tests.**
  - `TestCreateTopic_InvalidFolderName_422`, `TestCreateTopic_FolderNameWithDeleteData_422`.
  - `TestUpdateTopic_OmittedFolderFields_PassesNilPolicy` (fake repo records `folder == nil`).
  - `TestUpdateTopic_ModeOnly_KeepsStoredName`: existing custom "X", PUT `{"folder_name_mode":"custom"}` → policy `{custom, "X"}`.
  - `TestUpdateTopic_TurnOnFolderName_WithStoredDeleteData_422`.
- [ ] **Step 2: Implement.** Update: start from `existing` values, apply pointers, run `NormalizeFolderName`, then `ValidUpdatePolicy` on the resulting values; pass a non-nil policy only when either pointer was sent. Map both new errors to 422 in `topicCreateProblem` and in Update.
- [ ] **Step 3: gofmt.**
- [ ] **Step 4: Commit** `feat(api): accept folder name settings on topics`.

### Task 4: Capability, torrent helpers, metric

**Files:**
- Modify: `backend/internal/plugins/registry/registry.go` (after `WithFileSelection`)
- Modify: `backend/internal/torrentmeta/torrentmeta.go`, `torrentmeta_test.go`
- Modify: `backend/internal/metrics/metrics.go` (next to `SchedulerFileSelectionTotal`)
- Modify: `backend/internal/api/handlers/system.go` (`listClientInfos`), `system_test.go`

**Interfaces:**
- Produces:
  - `registry.WithRename` exactly as the spec (`RenameRoot`, `Recheck`, `BytesDone`), embedding `Client`.
  - `torrentmeta.RootName(data []byte) (name string, multiFile bool, err error)` — `info.name` (prefer `name.utf-8`), `multiFile` = `info.files` present.
  - `torrentmeta.ClientRoot(files []domain.ClientFile) (string, bool)` — the single first path component shared by every non-padding file that has ≥2 components; reuse `rootStripper`'s rule.
  - `metrics.SchedulerFolderRenameTotal` CounterVec, labels `client`, `result`.
  - `/system/info` `clients[].supports_rename bool` — true only when the plugin implements both `WithRename` and `WithFileSelection`.

- [ ] **Step 1: Failing tests** `TestSystemInfo_SupportsRename`; `TestRootName` (multi-file, single-file, `name.utf-8` preferred, garbage → error) using `torrentmetatest`; `TestClientRoot` (rooted `Show/E01.mkv`+`Show/E02.mkv` → `"Show", true`; verbatim `E01.mkv` → false; mixed roots → false; padding under the root ignored).
- [ ] **Step 2: Implement.**
- [ ] **Step 3: gofmt; Commit** `feat(registry): add rename capability and root helpers`.

### Task 5: qBittorrent and Transmission `WithRename`

**Files:**
- Create: `backend/internal/plugins/clients/qbittorrent/rename.go`, `rename_test.go`
- Create: `backend/internal/plugins/clients/transmission/rename.go`, `rename_test.go`

**Interfaces:**
- Consumes: `registry.WithRename` (Task 4).
- Produces: both plugins satisfy `registry.WithRename` (add `var _ registry.WithRename = (*plugin)(nil)`).

- [ ] **Step 1: Failing httptest tests.**
  - qBittorrent: `RenameRoot` POSTs `torrents/renameFolder` with form `hash`, `oldPath=Old`, `newPath=New`; non-200 → error containing the status. `Recheck` POSTs `torrents/recheck` `hashes=<h>`. `BytesDone` GETs `torrents/info?hashes=<h>` and returns `downloaded`; empty array → error `torrent not found`.
  - Transmission: `RenameRoot` sends `torrent-rename-path` `{ids:[h], path:"Old", name:"New"}`; `result != "success"` → error. `Recheck` sends `torrent-verify {ids:[h]}`. `BytesDone` sends `torrent-get {fields:["hashString","downloadedEver"], ids:[h]}`; empty `torrents` → error.
- [ ] **Step 2: Implement** using each plugin's existing session/`do` helpers (see `fileselection.go` in each package).
- [ ] **Step 3: gofmt; Commit** `feat(clients): rename and recheck in qBittorrent and Transmission`.

### Task 6: Deluge `WithRename`

**Files:**
- Create: `backend/internal/plugins/clients/deluge/rename.go`, `rename_test.go`

- [ ] **Step 1: Failing tests:** `RenameRoot` calls `core.rename_folder` with params `[h, "Old/", "New/"]` (trailing slash added when missing, not doubled); `Recheck` calls `core.force_recheck` with `[[h]]` (a list); `BytesDone` calls `core.get_torrent_status` `[h, ["total_done"]]`, `{}` → error `torrent not found`.
- [ ] **Step 2: Implement; gofmt; Commit** `feat(clients): rename and recheck in Deluge`.

### Task 7: Scheduler step

**Files:**
- Create: `backend/internal/scheduler/folder_name.go`, `folder_name_test.go`
- Modify: `backend/internal/scheduler/update_policy.go` (`selectNewFiles` takes the client list; `finishDelivery` orchestration; `deliveryPlan` gains `folder *folderPlan`)
- Modify: `backend/internal/scheduler/scheduler.go` (`deliveriesRecorder` + `LatestOther`; optional `folderNameSeenSetter` interface; `sendViaClient` passes `cfg` to the plan)
- Modify: `backend/internal/scheduler/update_policy_test.go` (callers of the changed `selectNewFiles`)

**Interfaces:**
- Consumes: Tasks 1, 4.
- Produces (package-internal):
  - `type folderNameSeenSetter interface { SetFolderNameSeen(ctx context.Context, id uuid.UUID, name string) error }` — type-asserted on `s.topics`, like `displayNamePersister`.
  - `type folderPlan struct { renamer registry.WithRename; selector registry.WithFileSelection; hash, from, want string; result, note string }` — `result`/`note` set for "cannot apply" outcomes decided at plan time.
  - `func (s *Scheduler) planFolderName(ctx, log, t *domain.Topic, episodic bool, cfg *domain.Client, rawConfig []byte, clientPlugin registry.Client, payload *domain.Payload) *folderPlan` — nil means nothing to do.
  - `func (s *Scheduler) applyFolderName(ctx, log, t, clientName string, rawConfig []byte, fp *folderPlan, clientFiles []domain.ClientFile) (files []domain.ClientFile, note string, keepPaused bool)`.
  - `func (s *Scheduler) selectNewFiles(ctx, log, t, clientName string, rawConfig []byte, sel *fileSelection, clientFiles []domain.ClientFile) (note string, ok bool)`.
  - `func joinNotes(notes ...string) string` — skips empty, joins with one space.
  - `const folderSelectionTimeout = 30 * time.Second` replaces `fileSelectionTimeout` as the shared budget.

Plan order is the spec's "Plan" list 1–7; keep-mode live read only when `LatestOther`'s `ClientID` equals `cfg.ID` (other client → use `FolderNameSeen`). Plan sets `deliveryPlan.paused = true` when `folder` needs a rename. After `Add`, `finishDelivery` does: one `waitForFiles` (only if a selection or a folder rename needs it) → `applyFolderName` → `selectNewFiles` with the returned list → start decision. `Start` is called once, at the end, only when neither the update policy nor a failed rename keeps it paused; when the folder plan alone paused the torrent (no update policy), the end step calls `Start` via the `WithFileSelection` selector.

- [ ] **Step 1: Failing tests** (fake client implementing `WithFileSelection` + `WithRename`, recording calls in order):
  - `TestPlanFolderName_Table`: default mode → nil; episodic → nil; magnet → `magnet` note; single-file → `single_file`; client without `WithRename` → `unsupported`; custom equal to `info.name` → nil; custom different → rename plan, paused.
  - `TestPlanFolderName_Keep_LiveRead`: previous delivery on same client with root `"Renamed"` → want `"Renamed"`, `SetFolderNameSeen("Renamed")` called.
  - `TestPlanFolderName_Keep_ExcludesOwnHash`: `LatestOther` called with the payload's infohash.
  - `TestPlanFolderName_Keep_FallsBackToSeen`: previous on another client, or `Files` returns error/empty → want = `FolderNameSeen`; seen empty → nil.
  - `TestApplyFolderName_Order`: calls are `BytesDone`, `RenameRoot(from→want)`, `Files`, `Recheck`, then `Start`; note `Saved in folder "X".`
  - `TestApplyFolderName_SameName_SkipsRenameAndRecheck` (live root already `want`).
  - `TestApplyFolderName_HasData` (BytesDone 5 → no rename, `has_data` note, started).
  - `TestApplyFolderName_NoRoot` (verbatim list → `no_root` note).
  - `TestApplyFolderName_RenameFails_LeftPaused` (no `Start`, failure note names the step, raw error absent from note).
  - `TestFinishDelivery_FolderAndOnlyNewFiles`: selection uses the list read after rename; notes joined policy-first.
  - `TestFinishDelivery_Keep_SavesSeenAfterRename`.
  - `TestJoinNotes`.
- [ ] **Step 2: Implement.** Metric incremented once per outcome with the result names from Global Constraints.
- [ ] **Step 3: gofmt; Commit** `feat(scheduler): apply topic folder name on delivery`.

### Task 8: Real-client check

**Files:**
- Modify: `backend/internal/plugins/clients/fileselectioncheck/fileselection_test.go` (new test func in the same file, same `targets()`)
- Modify: `deploy/docker-compose.test-clients.yml` only if Transmission needs `incomplete-dir-enabled: true` set explicitly for the second case.

- [ ] **Step 1: Write** `TestRenameOnRealClients` (`-tags=clientcheck`), per target: (1) add a `TEST-225-a` multi-file torrent paused, `BytesDone == 0`, `RenameRoot` to `TEST-225-renamed`, `Files` shows the new root; (2) seed data for torrent A under `TEST-225-renamed`, add torrent B (shares files) paused, rename into the same folder, `Recheck`, assert B's progress > 0 for the shared files; (3) `Start` during recheck → torrent ends not paused; (4) `RenameRoot` to its current name returns nil or a recognisable error the scheduler never hits (`same_name` path).
- [ ] **Step 2: Ask the user to run it** (it is a real-container run, not CI):
  `docker compose -p marauder225check -f deploy/docker-compose.test-clients.yml up -d --wait` then the Docker `golang:1.26` command with `go test -tags=clientcheck ./internal/plugins/clients/fileselectioncheck/ -run TestRenameOnRealClients -v`, then `docker compose -p marauder225check ... down -v`. If Deluge fails, remove Deluge's `WithRename` (Task 6) and note it in the docs.
- [ ] **Step 3: Commit** `test(clients): prove rename and recheck on real clients`.

### Task 9: Frontend

**Files:**
- Create: `frontend/src/components/topics/FolderNameFields.tsx`, `FolderNameFields.test.tsx`
- Create: `frontend/src/components/topics/useRenameUnsupported.ts`
- Modify: `frontend/src/lib/api.ts` (create/update request types, `Topic` type, `SystemInfo.clients[].supports_rename`)
- Modify: `frontend/src/components/topics/UpdatePolicyFields.tsx` (+ test) — new prop `deleteDataLocked: boolean` replacing the `onlyNewFiles`-only lock
- Modify: `TopicForm.tsx`, `AddTopicCard.tsx`, `EditTopicCard.tsx`, `i18n/en.ts`, `i18n/ru.ts`

**Interfaces:**
- Produces: `interface FolderNameValue { folderNameMode: "default" | "custom" | "keep"; folderName: string }`; `FolderNameFields({ value, onChange, unsupportedClient })`; `useRenameUnsupported(clients, effectiveClientId): string | null`.

- [ ] **Step 1: Failing tests.**
  - `FolderNameFields.test.tsx`: select shows three options; text input appears only for custom; amber warning only when `unsupportedClient` set and mode ≠ default.
  - `UpdatePolicyFields.test.tsx`: with `deleteDataLocked` the delete-data box is unchecked and disabled.
  - Extend `TopicFormUpdatePolicy.test.tsx`: custom mode submits `folderNameMode: "custom"`, `folderName`, and `replaceDeleteData: false`; per-episode tracker submits `folderNameMode: "default"`.
- [ ] **Step 2: Implement.** In `TopicForm`, add the two values to the `delivery` state object, render `<FolderNameFields>` right after `<UpdatePolicyFields>` (so it is already hidden for notify-only) and only when `!episodic`; pass `deleteDataLocked={delivery.onlyNewFiles || delivery.folderNameMode !== "default"}`; submit `replaceDeleteData && !locked` and `folderNameMode: episodic ? "default" : ...`. Choosing a non-default mode also sets `replaceDeleteData: false` (mirror the only-new-files toggle). en + ru strings under `topics.folderName.*`.
- [ ] **Step 3: prettier --write** each changed file.
- [ ] **Step 4: Commit** `feat(frontend): folder name setting in topic form`.

### Task 10: Docs

**Files:**
- Create: `docs/folder-name.md`
- Modify: `CLAUDE.md` (repo `Topics` columns, registry capability list, new "Folder name (issue #225)" paragraph under Scheduler design, client plugin line), `CHANGELOG.md` `[Unreleased]`

- [ ] **Step 1: Write** the user guide with the three warnings from the spec's Docs section, plus "magnets and single-file torrents keep the tracker's name" and which clients support it.
- [ ] **Step 2: Commit** `docs: describe the topic folder name setting`.

### Task 11: Pull request and CI

- [ ] **Step 1:** Push the branch (name must start with `225-`; rename `feat/issue-225` → `225-custom-folder-name` before the first push) — only with the user's go-ahead.
- [ ] **Step 2:** Open the PR: title `feat(topics): custom torrent folder name`, body ends with `Ref #225`, no closing keywords, no AI attribution.
- [ ] **Step 3:** `gh pr checks <n> --watch`; on red, `gh run view <id> --log-failed`, fix, push, repeat until green.
