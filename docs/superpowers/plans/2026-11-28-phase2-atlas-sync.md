# Awesome Reminder — Phase 2 (Atlas Sync) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Optional sync of items, image attachments and reminder defaults across machines through the user's own MongoDB Atlas cluster, with restore on a fresh machine and sync status shown on the tray icon, in the list header, and on each row that has not reached Atlas yet.

**Architecture:** SQLite stays the working store. Store migration v3 adds an `outbox`, and every local write lands in it in the same transaction. A backend-agnostic engine (`internal/cloudsync`) pushes the outbox and pulls by a server-stamped watermark through a `Syncer` interface. `internal/cloudsync/atlas` implements that interface with the official Mongo driver. Core gains last-writer-wins "apply remote" operations. The Atlas URI lives in the OS keychain (`internal/secret`), with an encrypted-file fallback. The app layer owns a sync controller (connect, disconnect, startup), a `sync:status` event, the tray status dot, the list header pill and the Settings › Sync section.

**Tech Stack:** Go (toolchain per `go.mod`) · Wails v3.1.0 · vanilla TS + Vite · `go.mongodb.org/mongo-driver/v2` · `github.com/zalando/go-keyring` (both pinned to exact versions at install; neither needs CGo) · stdlib `crypto/aes` + `crypto/cipher` (GCM) · `vitest` + `jsdom` (existing)

**Spec:** `docs/superpowers/specs/2026-11-27-awesome-reminder-design.md` (Storage & sync, Data model, Sync rules, Constraints, Delivery phase 2 incl. "Sync status everywhere"), `docs/superpowers/specs/2026-11-28-task-details-priority-design.md` ("Phase 2 (sync) impact"), `docs/superpowers/specs/2026-11-27-ui-design-guide.md`.

**Precondition:** Phases 1, 1.5 and 1.6 are merged into `master` (store schema v2, attachments, pending badge). Branch from current `master`. The README roadmap on `master` (87ea342) puts hands-free voice in Phase 3 and ntfy push in Phase 4. Neither is in scope here, and nothing in this plan is designed around them.

**Decisions made on 2026-11-28** (the spec left them open, or this plan changes the spec; the user answered, then left the final calls to the plan author):
- **Synced settings are the reminder defaults only:** lead times, default time, "next week" day and time. The hotkey, export path, autostart and "show pending count" stay per machine. This overrides the spec's list, which also syncs the hotkey and export path.
- **The Atlas user's scope is checked and warned about, never blocked.** The user first said not to check. With the final call left to the plan author, the plan adds a cheap check, because the cluster hosts other projects. On the first cycle the app reads the user's privileges (`connectionStatus` with `showPrivileges`). If the user can reach any database other than `awesome_reminder`, sync still runs and Settings and the sync details show a persistent warning. A failed check is logged, not shown. The setup guide still explains how to scope the user.
- **No OS keychain** (e.g. Linux without a Secret Service): the URI goes to a file instead. The user asked for it encrypted "by SHA". SHA is a one-way hash, so the app could never read the URI back from it. The plan uses AES-256-GCM with a random per-install key instead. The key lives in its own file and the ciphertext in the database, so a copied database (a backup, or one attached to a bug report) does not leak the URI. Someone with full access to the user's account can still decrypt it. The Settings help text says so.
- **First connect merges silently:** the union of both sides, per-record LWW. A fresh machine is therefore a plain restore.

## Global Constraints

- Atlas database `awesome_reminder` only, collections `items`, `attachments`, `settings`. A database name in the URI path is ignored. Tests may override the database and a collection prefix; the app never does.
- Capture, the UI and startup never wait on the network. With no URI stored, no Mongo client is created and the app behaves exactly as in Phase 1.6.
- No client-side encryption of synced data. TLS comes from Atlas.
- The URI is stored in the OS keychain (service `dev.awesome-reminder.app`, account `atlas-uri`) and falls back to the AES-256-GCM file (`<DataDir>/sync.key` + meta `sync_uri_enc`). It never appears in logs, events or error strings. The UI shows only the host (`atlas.Host`).
- **LWW:** the newer `updated_at` (compared at millisecond precision) wins; on a tie the lexicographically greater `updated_by` (device ULID) wins. Deletes are tombstones, and nothing is purged in Phase 2.
- **Push** writes Atlas with `$set` of the known fields plus `$currentDate: {_srv: {$type: "date"}}`, conditionally on LWW. It never replaces a document, so fields written by anything else (e.g. a newer app version) survive.
- **Pull** reads by the server stamp `_srv`, ordered by `(_srv, _id)`, re-reading a 2 min overlap before the saved watermark. Page size is 200 items, or 8 attachments (attachments carry their bytes).
- **Cadence:**
  - poll every 30 s;
  - push 2 s after a local change;
  - Offline backoff 30 → 60 → 120 → 240 → 300 s (cap);
  - Error retry every 5 min;
  - "Sync now" runs immediately and resets the backoff.
- **Mongo client:** `maxPoolSize 2`, `minPoolSize 0`, `maxConnIdleTime 90s`, `heartbeatInterval 60s`, `serverSelectionTimeout 10s`, `connectTimeout 10s`, `appName "awesome-reminder"`.
- Idle RAM stays < 150 MB with sync on (Phase 1 measured 137 MB). Measure as in `docs/manual-test-checklist.md` row 6.
- **Synced documents:**
  - **items:** `_id, kind, text, tags, due_at, tz, rrule, lead_times (ms array or null), status, done_at, snooze_until, created_at, updated_at, updated_by, priority, description, _srv`.
  - **attachments:** `_id, item_id, mime, width, height, bytes, sha256, data (BinData, unset on tombstones), created_at, updated_at, updated_by, deleted, _srv`.
  - **settings:** `_id: "settings", lead_times, default_time, next_week_day, next_week_time, updated_at, updated_by, _srv`.
  - **Local only:** `unreferenced_since`, `notify_log`, `device_id`, the outbox, watermarks, the URI.
- **Status vocabulary** (exact strings). The machine states are `off | syncing | synced | offline | error`.
  - **Tray tooltip suffixes:** `Synced · 2 min ago`, `Syncing…`, `Offline · N changes waiting` (`1 change waiting`; just `Offline` at 0), `Sync error: <reason>`; nothing when off.
  - **List pill:** `All synced · just now` / `All synced · 2 min ago`, `Synced · N waiting`, `Syncing…`, `Offline · N changes waiting`, `Sync error`.
  - **Row marker** `aria-label`: `Not synced yet`.
- Scope warning text (exact): `This Atlas user can reach other databases on your cluster. Limit it to readWrite on awesome_reminder (see the setup guide).`
- Desktop pop-ups still appear on every running machine (`notify_log` stays local). Done, Snooze and Delete on one machine reach the others through item sync.
- UI work follows the UI design guide checklist: tokens only in `tokens.css`, Lucide icons, contrast ≥ 4.5:1 in both themes, keyboard-complete, `prefers-reduced-motion`.
- Never run `bin/awesome-reminder.exe` from its repo path. Use a copy under `%TEMP%` with `APPDATA` pointed at a temp dir.

## Review Focus

1. **Restoring onto a fresh machine** with dozens of overdue open reminders must not pop a "missed" card for each. Items that a pull *inserts* have their already-due alerts marked as fired. Items it *updates* keep normal scheduler rules. → Task 3 `TestApplyRemoteNewItemSuppressesPastAlerts`, Task 5 `TestRestoreFreshDevice`.
2. **An edit made while its push is in flight**, or after days offline, must never be lost or marked synced early. The outbox row is acknowledged only if its `seq` is unchanged. → Task 1 `TestPutEnqueuesAndAckBySeq`, Task 5 `TestEditDuringPushStaysPending`.
3. **Two machines editing the same item**, including in the same millisecond, must agree on one winner on both sides. A clock more than 2 min off is called out. → Task 5 `TestConcurrentEditLWW`, `TestClockSkewWarning`.
4. **Text that is special to Mongo or to the app** (`$HOME`, `a.b`, `{"$gt":1}`, Markdown with `att:` refs) round-trips byte-exact. A malformed remote document is skipped without stalling sync. → Task 5 conformance row `TestSpecialTextRoundTrips`, `TestInvalidRemoteDocDoesNotStall`.
5. **Network loss mid-sync, a paused cluster, or a wrong password** each produce the right state and wording, with no tight retry loop. → Task 6 `TestClassify`, Task 5 `TestNextDelay`.

---

### Task 1: Store v3 — outbox, meta, remote writes

**Files:**
- Modify: `internal/store/migrate.go`, `internal/store/store.go`, `internal/store/attachments.go`, `internal/store/store_test.go`, `internal/store/attachments_test.go`
- Create: `internal/store/outbox.go`, `internal/store/outbox_test.go`

**Interfaces:**
- Produces:
  ```go
  const (OutboxItem = "item"; OutboxAttachment = "attachment"; OutboxSettings = "settings") // settings id is "settings"
  type OutboxEntry struct { Kind, ID string; Seq int64 }
  func (s *Store) Put(it model.Item) error                    // now also enqueues (item, id) in the same tx
  func (s *Store) PutAttachment(a model.Attachment) error     // now also enqueues (attachment, id)
  func (s *Store) PutRemote(it model.Item) error              // no enqueue; deletes the item's outbox row (remote won)
  func (s *Store) PutAttachmentRemote(a model.Attachment) error
  func (s *Store) SaveSettingsShared(st settings.Settings) error // SaveSettings + enqueue (settings, "settings") in one tx
  func (s *Store) Outbox(limit int) ([]OutboxEntry, error)    // attachments first, then items, then settings; rowid order within a kind
  func (s *Store) Ack(e OutboxEntry) (bool, error)            // deletes only if seq still matches
  func (s *Store) OutboxCount() (int, error)
  func (s *Store) PendingItemIDs() ([]string, error)          // items in the outbox ∪ item_id of attachments in it, distinct, sorted
  func (s *Store) EnqueueAll() error                          // every item, every attachment, and settings if a row exists
  func (s *Store) Meta(key string) (string, bool, error)
  func (s *Store) SetMeta(key, value string) error
  func (s *Store) DeleteMeta(key string) error
  ```
  Migration index 2 (v2→v3): `CREATE TABLE outbox (kind TEXT NOT NULL, id TEXT NOT NULL, seq INTEGER NOT NULL, PRIMARY KEY(kind,id))`, then seed one row (seq 1) per existing item, per attachment, and for the settings row if present. Enqueue is `INSERT … ON CONFLICT(kind,id) DO UPDATE SET seq = outbox.seq + 1`. The outbox fills even while sync is off, bounded by the record count. That way, first connect needs no special path.

- [ ] **Step 1: Write failing tests**
  - `TestMigrateV2ToV3SeedsOutbox`: build a v2 DB with 2 items, 1 attachment and saved settings, then `Open` it. Assert schema_version `3`, `OutboxCount() == 4`, and data unchanged.
  - `TestPutEnqueuesAndAckBySeq`: Put → entry seq 1. Put again → seq 2. `Ack({item,id,1})` → false and the row stays. `Ack({…,2})` → true and the count is 0.
  - `TestPutRemoteDoesNotEnqueueAndClearsPending`: Put (pending), then PutRemote of the same id → count 0. PutRemote of a new id → count 0.
  - `TestOutboxOrder`: an item, an attachment and settings put in that order are returned as attachment, item, settings.
  - `TestPendingItemIDsIncludesAttachmentOwners`.
  - `TestMetaRoundTrip` (missing → `ok=false`; set, get, delete).
  - `TestEnqueueAll` (after acking everything, EnqueueAll restores the full count).
  - Extend `TestMigrateIdempotent`: the version stays `3`.
- [ ] **Step 2:** Run `go test ./internal/store/ -v`. Expected: FAIL.
- [ ] **Step 3:** Implement. `Put`, `PutAttachment` and `SaveSettingsShared` each run their write and the enqueue in one `db.Begin()` tx (the pool has a single connection, so nothing else may run on `s.db` inside the tx).
- [ ] **Step 4:** Run `go test ./internal/...`. Expected: PASS. Update tests that assert schema version `2`.
- [ ] **Step 5:** Commit `feat(store): v3 outbox, meta and remote writes`.

---

### Task 2: Shared settings subset with its own stamp

**Files:**
- Modify: `internal/settings/settings.go`, `internal/settings/settings_test.go`, `internal/core/core.go` (`SaveSettings`), `internal/core/core_test.go`, `frontend/bindings/**` (regenerated), `frontend/src/settings.ts` (carry the new fields through `toSettings`)

**Interfaces:**
- Consumes: Task 1 `SaveSettingsShared`, `Outbox`.
- Produces:
  ```go
  // settings
  type Shared struct { LeadTimes []time.Duration; DefaultTime, NextWeekDay, NextWeekTime string }
  func (s Settings) Shared() Shared
  func (s Settings) WithShared(p Shared) Settings // replaces only the shared fields
  func (p Shared) Equal(q Shared) bool
  // Settings gains (JSON "sharedUpdatedAt" as unix ms, "sharedUpdatedBy"; missing → zero / ""):
  SharedUpdatedAt time.Time
  SharedUpdatedBy string
  ```
  `core.SaveSettings(s)` ignores the stamp fields the caller sends and keeps the stored ones. Only when `s.Shared()` differs from the stored `Shared()` does it stamp `now`/device id and call `SaveSettingsShared`; otherwise it calls `SaveSettings`. Validation and OnChange behave as before.

- [ ] **Step 1: Write failing tests**
  - `TestWithSharedKeepsLocalFields`: hotkey, exportPath, autostart and showPendingCount are unchanged; lead times, default time and next-week fields are replaced.
  - `TestSettingsJSONMissingSharedStamp`: old JSON → zero time and `""`; a round-trip keeps a set stamp.
  - `TestSaveSettingsStampsOnlySharedChanges`: after acking the outbox, changing only the hotkey gives no settings outbox row and an unchanged stamp; changing the lead times gives a stamp equal to the fake now, `SharedUpdatedBy == DeviceID()`, and one settings outbox row; a stamp sent by the caller is ignored.
- [ ] **Step 2:** Run `go test ./internal/settings/ ./internal/core/ -v`. Expected: FAIL.
- [ ] **Step 3:** Implement. Extend the `wire` struct (`SharedUpdatedAt *int64`). Regenerate bindings with the Taskfile's binding step, as in earlier phases. `settings.ts` passes the loaded stamp fields back unchanged.
- [ ] **Step 4:** Run `go test ./internal/... && npm --prefix frontend run typecheck && npm --prefix frontend test`. Expected: PASS.
- [ ] **Step 5:** Commit `feat(settings): shared subset with its own update stamp`.

---

### Task 3: Core sync surface — LWW apply, readers, pending view, restore alert suppression

**Files:**
- Create: `internal/core/sync.go`, `internal/core/sync_test.go`
- Modify: `internal/model/item.go` (`Newer`), `internal/core/view.go` + `core.go` (`View.Pending`), `internal/core/attachments.go` (share the validation and atomic write)

**Interfaces:**
- Consumes: Task 1 store API, Task 2 `settings.Shared`, `scheduler.Plan` (core may import scheduler; scheduler does not import core).
- Produces:
  ```go
  // model
  func Newer(aAt time.Time, aBy string, bAt time.Time, bBy string) bool // ms precision; tie → aBy > bBy
  // core
  type Applied struct { Before *model.Item; After model.Item } // Before nil = inserted on this device
  var ErrAttachmentFileMissing = errors.New("attachment file missing")
  func (c *Core) ApplyRemoteItems(items []model.Item) ([]Applied, error)
  func (c *Core) ApplyRemoteAttachment(a model.Attachment, data []byte) (bool, error)
  func (c *Core) ApplyRemoteShared(p settings.Shared, at time.Time, by string) (bool, error)
  func (c *Core) SharedForSync() (settings.Shared, time.Time, string, error)
  func (c *Core) AttachmentForSync(id string) (model.Attachment, []byte, error) // data nil for tombstones; ErrAttachmentFileMissing for a live row without a file
  func (c *Core) Outbox(limit int) ([]store.OutboxEntry, error)
  func (c *Core) Ack(e store.OutboxEntry) error
  func (c *Core) OutboxCount() (int, error)
  func (c *Core) EnqueueAll() error
  func (c *Core) SyncMeta(key string) (string, bool, error)
  func (c *Core) SetSyncMeta(key, value string) error
  func (c *Core) DeleteSyncMeta(key string) error
  // View gains:
  Pending []string `json:"pending"` // PendingItemIDs; [] never null
  ```
- Rules:
  - **`ApplyRemoteItems`** runs under `opMu`. It validates each item and skips (with a log line) any that fails: ULID id; kind todo/reminder; status open/done/deleted; `model.ValidPriority`; non-empty tags; a reminder must have `DueAt`, a to-do must not; `TZ` is `""` or loads.
    - A valid item is written with `PutRemote` when there is no local copy or `Newer(remote, local)`.
    - For inserted, open items with a due time, run `scheduler.Plan([]model.Item{it}, leads, now, never)` and `MarkNotified` every `covered` key. This is Review Focus 1.
    - `changed()` fires once per call if anything was applied.
    - Remote `UpdatedAt`/`UpdatedBy` are kept verbatim; never call `save`.
  - **`ApplyRemoteAttachment`** validates: ULID ids, mime `image/webp`, and for live rows `len ≤ 2 MB`, `webp.DecodeConfig` succeeding, and `sha256(data) == a.SHA256`. When the remote row is newer, a live row writes the file atomically (skipped if the file exists with the same sha); a tombstone removes the file. Both then call `PutAttachmentRemote`, keeping the local `UnreferencedSince` (nil for a new row, so a pulled image is never swept by the 7-day rule on the pulling machine). It fires `changed()` when applied.
  - **`ApplyRemoteShared`**, when `Newer(at, by, local stamp)`, saves `local.WithShared(p)` with the remote stamp, validates first (invalid → `false`, logged), and drops the settings outbox row. It fires `changed()`.

- [ ] **Step 1: Write failing tests** (fixed clock, `:memory:` store):
  - `TestNewer` (ms truncation: 10:00:00.0004 vs 10:00:00.0001 is a tie; the device tie-break).
  - `TestApplyRemoteItemsLWW`: newer → applied; older → skipped; equal time with a greater device → applied; with a smaller device → skipped.
  - `TestApplyRemoteClearsPendingOnlyWhenRemoteWins`.
  - `TestApplyRemoteSkipsInvalid`: bad id, kind `"x"`, priority `"urgent"`, a reminder without a due time → skipped; the valid ones in the same batch are applied.
  - **`TestApplyRemoteNewItemSuppressesPastAlerts`**:
    - an inserted reminder due 1 h ago with leads [1h,10m] → `scheduler.Plan` over the store with `WasNotified` gives no `fireNow`;
    - an inserted reminder due in 2 h keeps its future alerts (`next` non-zero);
    - an *existing* item updated remotely to a due time 5 min ago still yields its missed at-time alert.
  - `TestApplyRemoteAttachment`:
    - stores the file and row;
    - a sha mismatch or a PNG body → error, nothing written;
    - a newer tombstone removes the file;
    - an older remote is ignored;
    - the local `UnreferencedSince` is kept.
  - `TestApplyRemoteShared`: newer → lead times change and the hotkey is kept; older → ignored; `DefaultTime "25:00"` → false.
  - `TestAttachmentForSync` (tombstone → nil data; missing file → `ErrAttachmentFileMissing`).
  - `TestViewPending`.
  - `TestApplyBatchFiresOnChangeOnce`.
- [ ] **Step 2:** Run `go test ./internal/core/ ./internal/model/ -v`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `go test ./internal/...`. Expected: PASS.
- [ ] **Step 5:** Commit `feat(core): last-writer-wins remote apply and sync readers`.

---

### Task 4: Secret storage — OS keychain with encrypted-file fallback

**Files:**
- Create: `internal/secret/secret.go`, `internal/secret/file.go`, `internal/secret/secret_test.go`
- Modify: `go.mod`/`go.sum` (`github.com/zalando/go-keyring`)

**Interfaces:**
- Produces:
  ```go
  var ErrNotFound = errors.New("no connection string saved")
  type MetaStore interface { Meta(string) (string, bool, error); SetMeta(k, v string) error; DeleteMeta(string) error } // *store.Store satisfies it
  type Store interface {
      Get() (string, error) // ErrNotFound when unset
      Set(v string) error
      Delete() error        // clears the keychain entry, the ciphertext and the key file; missing pieces are not errors
      Kind() string         // "Windows Credential Manager" | "macOS Keychain" | "Secret Service" | "Encrypted file on this computer"
  }
  func New(dataDir string, meta MetaStore) Store
  ```
- Rules:
  - `Set` tries the keychain first. If the keychain errors (anything other than not-found), it writes the file fallback instead and clears any stale keychain entry.
  - `Get` reads the keychain, then the fallback.
  - The fallback key is 32 random bytes in `<dataDir>/sync.key`, written with mode `0600`, created on first `Set`.
  - The ciphertext is stored in meta `sync_uri_enc` as `v1:` + base64(nonce‖AES-256-GCM(ciphertext)), with additional data `"awesome-reminder atlas-uri v1"`.
  - Decrypt failure → `errors.New("the saved connection string can't be read; paste it again")`.
  - The keychain is injected through an unexported interface, so tests never touch the real OS keychain.

- [ ] **Step 1: Write failing tests:**
  - `TestKeychainPreferred` (the fake keychain holds the value; meta stays empty).
  - `TestFallbackWhenKeychainUnavailable`: the fake returns a D-Bus-style error. Meta holds a `v1:` value that doesn't contain the URI or its password; `Get` returns the URI; `Kind()` is the encrypted-file string.
  - `TestFallbackTamperedFails` (flip one ciphertext byte → the "paste it again" error).
  - `TestFallbackKeyFileMissing` (same error).
  - `TestDeleteClearsEverything`.
  - `TestGetUnsetIsErrNotFound`.
- [ ] **Step 2:** Run `go test ./internal/secret/ -v`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `go test ./internal/... && go vet ./...`. Expected: PASS.
- [ ] **Step 5:** Commit `feat(secret): keychain storage with encrypted-file fallback`.

---

### Task 5: Sync engine, `Syncer` interface, in-memory backend, conformance suite

**Files:**
- Create: `internal/cloudsync/syncer.go` (interface and types), `internal/cloudsync/engine.go`, `internal/cloudsync/engine_test.go`, `internal/cloudsync/memsync/memsync.go`, `internal/cloudsync/cloudsynctest/conformance.go`, `internal/cloudsync/memsync/memsync_test.go` (runs the conformance suite)

**Interfaces:**
- Consumes: Task 3 core methods; `store.OutboxEntry`.
- Produces:
  ```go
  type Watermark struct { T time.Time `json:"t"`; ID string `json:"id"` } // server stamp of the last pulled doc
  type RemoteAttachment struct { model.Attachment; Data []byte }
  type SharedDoc struct { Shared settings.Shared; UpdatedAt time.Time; UpdatedBy string }
  // Syncer is the backend (spec: "Sync is behind a Go Syncer interface").
  type Syncer interface {
      PushItem(ctx context.Context, it model.Item) error                          // LWW-conditional upsert; remote equal-or-newer → nil
      PushAttachment(ctx context.Context, a model.Attachment, data []byte) error  // data nil ⇒ tombstone, stored bytes cleared
      PushShared(ctx context.Context, d SharedDoc) error
      PullItems(ctx context.Context, after Watermark, limit int) ([]model.Item, Watermark, error)          // (_srv,_id) > after, ascending
      PullAttachments(ctx context.Context, after Watermark, limit int) ([]RemoteAttachment, Watermark, error)
      PullShared(ctx context.Context) (*SharedDoc, error) // nil when the cluster has none
      ServerTime(ctx context.Context) (time.Time, error)
      AttachmentBytes(ctx context.Context) (int64, error) // sum of bytes over live attachments
      ScopeTooBroad(ctx context.Context) (bool, error)    // true when the credentials reach databases other than the sync database
      Classify(err error) (State, string)                // Offline vs Error plus a user-facing reason
      Close(ctx context.Context) error
  }
  type State string // "off" | "syncing" | "synced" | "offline" | "error"
  type Status struct {
      State      State      `json:"state"`
      LastSyncAt *time.Time `json:"lastSyncAt"`
      Pending    int        `json:"pending"`
      Error      string     `json:"error"`   // reason when State == error (or offline detail)
      Warnings   []string   `json:"warnings"` // clock-skew and/or scope notes; [] never null
      Host       string     `json:"host"`
  }
  type Local interface { /* the Task 3 methods the engine calls; *core.Core satisfies it */ }
  type Options struct {
      Host string; Now func() time.Time
      Poll, Debounce, ErrorRetry time.Duration // defaults 30s, 2s, 5m
      OnStatus  func(Status)
      OnApplied func([]core.Applied)
  }
  func NewEngine(l Local, s Syncer, o Options) *Engine
  func (e *Engine) Start(ctx context.Context) // runs a manual-style first cycle (shows syncing), then polls
  func (e *Engine) Stop()                     // cancels, joins the cycle in flight, closes the Syncer
  func (e *Engine) Kick()                     // after Debounce, a cycle if OutboxCount() > 0
  func (e *Engine) SyncNow()                  // immediate cycle, shows syncing, resets the backoff
  func (e *Engine) Status() Status
  func (e *Engine) RunOnce(ctx context.Context, manual bool) error // one full cycle; tests and SyncNow use it
  func nextDelay(st State, failures int, o Options) time.Duration  // pure; see Global Constraints cadence
  ```
- Cycle algorithm (`RunOnce`):
  1. If `manual` or the outbox is non-empty, emit `syncing`. A quiet poll keeps the shown state, so the tray never flickers every 30 s.
  2. **Push:** loop `Outbox(100)` for at most 10 batches. For each entry, read it via `Item` / `AttachmentForSync` / `SharedForSync`, push it, then `Ack` it.
     - `ErrAttachmentFileMissing` and `store.ErrNotFound` → log and `Ack` (drop).
     - Any other error aborts the cycle.
  3. **Pull** attachments, then shared settings, then items (as shipped, so restored items use the remote lead times). Each collection starts from `saved.T − 2 min` (meta `sync_wm_attachments` / `sync_wm_items`, JSON) and pages until a short page. Each page goes through `ApplyRemote*`. Afterwards, save `max(saved, last returned)`. `OnApplied` receives the items' `Applied` lists.
  4. On success: state `synced`, `LastSyncAt = now` (also meta `sync_last_at`), and `Pending = OutboxCount()`. On error: `Classify`, then schedule `nextDelay`. Emit status either way.
  - On the first cycle after `Start`, `ServerTime` vs `Now`: if `|skew| > 2 min`, add to Warnings `This computer's clock is N min off. Sync keeps the most recent edit by clock time, so fix the clock to avoid losing edits.` Also on the first cycle: `ScopeTooBroad` true → add the scope warning (Global Constraints); an error → log only.
- `memsync` is an in-memory `Syncer` with the same LWW and `_srv` semantics: a server clock that strictly increases per write, plus `FailNext(err error, n int)`, `BeforePush(fn func())`, `PutRaw(doc map[string]any)` for malformed docs, and `SetScopeTooBroad(v bool, err error)`. Its `Classify` maps an injected `memsync.ErrNetwork` to offline and anything else to error.
- `cloudsynctest.Run(t *testing.T, open func(t *testing.T) cloudsync.Syncer)` holds the backend conformance tests, so memsync and Atlas are held to the same rules.

- [ ] **Step 1: Write failing conformance tests** in `cloudsynctest`:
  - `PushNewerOverwrites`, `PushOlderIgnored`, `PushEqualIsNoop`.
  - `TombstoneClearsData` (pull returns the tombstone with nil `Data`).
  - `PullOrderAndPaging` (5 docs, limit 2 → 3 pages, strictly ascending, no repeats).
  - `SharedLWW`.
  - **`TestSpecialTextRoundTrips`** (`$HOME`, `a.b`, `{"$gt":1}`, a 10 KB description with `![x](att:01J…)`, unicode).
  - `AttachmentBytesSumsLive`.
- [ ] **Step 2: Write failing engine tests** (two or three real cores on `:memory:` stores, one memsync, fake clock):
  - `TestTwoDevicesConverge` (A has 3 items, B has 2; sync A, B, A → identical ID/UpdatedAt sets).
  - **`TestConcurrentEditLWW`**: both edit offline; the later edit wins on both. With an equal ms stamp, the greater device id wins on both.
  - `TestDeleteTombstonePropagates`.
  - `TestOfflineQueueReplay`: three injected network errors give state offline and Pending 2; on recovery, Pending 0 and synced.
  - **`TestEditDuringPushStaysPending`** (`BeforePush` edits the item locally → after the cycle its outbox row remains; the next cycle pushes the newer text, which both devices end up with).
  - **`TestRestoreFreshDevice`**: A has items, an attachment and changed lead times. Fresh C → identical items, an attachment file with the same sha, A's lead times, C's own hotkey, and no `fireNow` for A's overdue reminders.
  - `TestPullOverlapIdempotent` (a second cycle with no changes → no `OnApplied`, no OnChange).
  - `TestPagingLargeBacklog` (450 items).
  - **`TestInvalidRemoteDocDoesNotStall`** (a malformed raw doc between valid ones → valid ones applied, watermark past it, next cycle quiet).
  - `TestQuietPollDoesNotShowSyncing`.
  - **`TestClockSkewWarning`** (server +7 min → `Warnings` holds the exact text with `7 min`).
  - `TestScopeWarning` (too broad → `Warnings` holds the exact scope text and state is still synced; a check error → no warning, still synced).
  - **`TestNextDelay`** table: offline failures 1..6 → 30 s, 60 s, 120 s, 240 s, 300 s, 300 s; error → 5 min; synced → 30 s.
- [ ] **Step 3:** Run `go test ./internal/cloudsync/... -v`. Expected: FAIL.
- [ ] **Step 4:** Implement. The engine runs one goroutine with one timer (no busy loop) and never holds core locks across network calls.
- [ ] **Step 5:** Run `go test -race ./internal/cloudsync/... ./internal/core/...`. Expected: PASS.
- [ ] **Step 6:** Commit `feat(cloudsync): outbox/pull sync engine with in-memory backend`.

---

### Task 6: Atlas backend

**Files:**
- Create: `internal/cloudsync/atlas/atlas.go`, `internal/cloudsync/atlas/docs.go` (BSON mapping), `internal/cloudsync/atlas/classify.go`, `internal/cloudsync/atlas/atlas_test.go`, `internal/cloudsync/atlas/integration_test.go`
- Modify: `go.mod`/`go.sum` (`go.mongodb.org/mongo-driver/v2`)

**Interfaces:**
- Consumes: Task 5 `Syncer`, `State`, `cloudsynctest.Run`.
- Produces:
  ```go
  const Database = "awesome_reminder"
  type Options struct { Database, Prefix string } // zero value = production; tests set both
  func Open(ctx context.Context, uri string, o Options) (*Syncer, error) // *Syncer implements cloudsync.Syncer
  func Host(uri string) string   // "cluster0.ab12c.mongodb.net"; "" if unparsable
  func Redact(uri string) string // password replaced by "••••"
  var ErrBadURI = errors.New("That isn't a MongoDB connection string. It should start with mongodb+srv://")
  ```
- Rules:
  - **`Open`:**
    - accepts only `mongodb+srv://` or `mongodb://` (else `ErrBadURI`, with no network use);
    - applies the client options from Global Constraints;
    - pings;
    - creates the `{_srv:1,_id:1}` indexes on items and attachments (`readWrite` allows it).
  - **Push** is `UpdateOne` with filter `{_id, $or:[{updated_at:{$lt:T}}, {updated_at:T, updated_by:{$lt:by}}]}` and update `{$set:{…}, $currentDate:{_srv:{$type:"date"}}}`, with `upsert`. `mongo.IsDuplicateKeyError` means the remote is equal or newer → nil.
    - An attachment tombstone also sends `$unset:{data:""}`.
    - Field names are exactly as in Global Constraints.
  - **Pull** is `Find({$or:[{_srv:{$gt:T}}, {_srv:T,_id:{$gt:ID}}]})` sorted by `{_srv:1,_id:1}`, with a limit and no projection, so attachment pulls carry `data`. Unknown fields are ignored on decode.
  - **`ServerTime`** is `hello.localTime`. **`AttachmentBytes`** is `$match {deleted:false}` + `$group $sum:"$bytes"`.
  - **`ScopeTooBroad`** runs `{connectionStatus: 1, showPrivileges: true}` and passes `authInfo.authenticatedUserPrivileges[].resource` to the pure `scopeTooBroad(resources []resource, db string) bool`. That is true when a resource has `db: ""` with a `collection` key (any database), or a `db` other than the sync database, `admin`, `local` or `config`. `{cluster: true}` resources are ignored.
  - **`Classify`:**

    | Error | State | User text |
    |---|---|---|
    | `ErrBadURI` | error | its text |
    | command code 18 (AuthenticationFailed) | error | `Atlas rejected the username or password.` |
    | code 13 or 8000 | error | `This database user can't read and write awesome_reminder.` |
    | `*net.DNSError`, `context.DeadlineExceeded`, `*net.OpError`, `io.EOF` | offline | `No connection to Atlas.` |
    | server-selection timeout, host resolves (`net.DefaultResolver.LookupHost`, 3 s) | error | `Can't reach your Atlas cluster. It may be paused (resume it in Atlas), or this network's IP isn't on the Atlas access list.` |
    | server-selection timeout, host doesn't resolve | offline | `No connection to Atlas.` |
    | anything else | error | `err.Error()` with the URI redacted |

- [ ] **Step 1: Write failing unit tests** (no server):
  - `TestHostAndRedact` (`mongodb+srv://u:p%40ss@cluster0.ab.mongodb.net/x?retryWrites=true` → host `cluster0.ab.mongodb.net`; the redacted form has no `p%40ss`).
  - `TestOpenRejectsBadScheme` (`postgres://…`, `""` → `ErrBadURI`, returns in < 100 ms).
  - **`TestClassify`** table, built from `mongo.CommandError{Code: 18}`, `{Code: 13}`, `&net.DNSError{}`, `context.DeadlineExceeded`, and a wrapped `ErrBadURI`. Assert the state and exact text for each, and that no output contains the password.
  - `TestScopeTooBroad` table: only `awesome_reminder` → false; plus `admin`/`local` → false; `{db:"", collection:""}` → true; `{db:"otherproj"}` → true; `{cluster:true}` only → false.
  - `TestDocMapping` (item ↔ BSON round-trip including nil `due_at`, nil `lead_times` vs `[]`, and ms precision).
- [ ] **Step 2: Write the integration test**, skipped unless `AWESOME_TEST_MONGO_URI` is set (database from `AWESOME_TEST_MONGO_DB`, default `awesome_reminder`; prefix `test_<ulid>_`; collections dropped in `t.Cleanup`):
  - `cloudsynctest.Run`;
  - `TestPushKeepsUnknownFields` (`$set` an unknown field `extra` directly, push a newer item → `extra` still present);
  - `TestTwoDevicesAgainstMongo` (two cores, two engines, converge).
- [ ] **Step 3:** Run `go test ./internal/cloudsync/atlas/ -v`. Expected: unit tests FAIL; integration SKIP.
- [ ] **Step 4:** Implement.
- [ ] **Step 5:** Run `go test ./...`. Expected: PASS. Then run the integration test once against a local `mongod` (`AWESOME_TEST_MONGO_URI=mongodb://localhost:27017`) and once against a real M0 with a user scoped to `awesome_reminder`. Record both in the commit message body; if a human must run the M0 pass, write "needs human".
- [ ] **Step 6:** Commit `feat(atlas): MongoDB Atlas Syncer`.

---

### Task 7: App wiring — sync controller, bindings, events, alert cards

**Files:**
- Create: `internal/app/syncctl.go`, `internal/app/syncctl_test.go`
- Modify: `internal/app/app.go`, `internal/app/service.go`, `internal/app/service_test.go`, `internal/app/alerts.go`, `internal/app/alerts_test.go`, `internal/app/events.go`, `frontend/bindings/**` (regenerated), `frontend/src/api.ts`

**Interfaces:**
- Consumes: Tasks 3–6.
- Produces:
  ```go
  const EventSyncStatus = "sync:status" // payload cloudsync.Status
  type SyncInfo struct {
      Connected       bool   `json:"connected"`
      Host            string `json:"host"`
      SecretStore     string `json:"secretStore"`     // secret.Store.Kind()
      AttachmentBytes int64  `json:"attachmentBytes"` // -1 when unknown (offline / not connected)
  }
  type syncDeps struct {
      Core      *core.Core
      Secret    secret.Store
      Open      func(ctx context.Context, uri string) (cloudsync.Syncer, error) // atlas.Open adapter; faked in tests
      Host      func(uri string) string
      OnStatus  func(cloudsync.Status)
      OnApplied func([]core.Applied)
  }
  func newSyncController(d syncDeps) *syncController
  func (c *syncController) Start(ctx context.Context) // URI saved → open in a goroutine; failures are classified and retried on nextDelay; returns at once
  func (c *syncController) Connect(ctx context.Context, uri string) (cloudsync.Status, error)
  func (c *syncController) Disconnect() error
  func (c *syncController) Kick(); SyncNow(); Stop()
  func (c *syncController) Status() cloudsync.Status // State off when no URI
  func (c *syncController) Info(ctx context.Context) SyncInfo
  // Service bindings:
  func (s *Service) SyncConnect(uri string) (cloudsync.Status, error)
  func (s *Service) SyncDisconnect() error
  func (s *Service) SyncStatus() cloudsync.Status
  func (s *Service) SyncNow()
  func (s *Service) SyncInfo() SyncInfo
  func (s *Service) remoteApplied(applied []core.Applied) // drops alert cards of items now not open, snoozed differently, or with a moved due time
  ```
- Behavior:
  - **`Connect`:**
    1. Trim the URI. `Open` it (validates and pings). On failure, return the classified text as the error; nothing is saved.
    2. `Secret.Set(uri)`. On failure, close the syncer and return `Couldn't save the connection string: <reason>`.
    3. The target is `Host(uri) + "/" + atlas.Database`. If it differs from meta `sync_target`, delete `sync_wm_*`, call `EnqueueAll`, and set `sync_target`.
    4. Replace any running engine and `Start` it, which runs the restore/merge cycle.
    5. Return the current status without waiting for the cycle.
  - **`Disconnect`** stops the engine, calls `Secret.Delete()`, and emits state `off`. Local data, the outbox and the watermarks stay, so reconnecting to the same cluster resumes.
  - **`app.go`:**
    - construct `secret.New(dataDir, st)` and the controller;
    - `c.OnChange` also calls `syncCtl.Kick()`;
    - `OnStatus` emits `EventSyncStatus` and calls `badges.SyncChanged()` (Task 8);
    - `OnApplied` calls `svc.remoteApplied`;
    - `syncCtl.Start(ctx)` runs in `ApplicationStarted` after the scheduler starts;
    - `syncCtl.Stop()` joins the shutdown block before `badges.Stop()`, so nothing touches the store after it closes.
  - **`api.ts`** gains typed wrappers and an `onSyncStatus(cb)` subscription.
- [ ] **Step 1: Write failing tests** (fake `Open` that returns memsync, fake `secret.Store`):
  - `TestSyncConnectBadURIStoresNothing`.
  - `TestSyncConnectSavesSecretAndResetsOnNewTarget` (first connect → `EnqueueAll` happened and the watermark is cleared; reconnecting to the same host keeps the watermark; a different host resets it).
  - `TestSyncDisconnectKeepsLocalData` (items and outbox intact; state off; secret empty).
  - `TestStartDoesNotBlockOnNetwork` (a fake `Open` blocked on a channel → `Start` returns in < 50 ms; state off until open succeeds).
  - `TestStartRetriesAfterOpenFailure` (open fails offline once, then succeeds on the next tick).
  - `TestRemoteDoneDropsAlertCards` (a queued card for item X; `remoteApplied` with X done → card gone; a remote edit of only the text → card kept).
  - `TestStatusNeverContainsURI` (a password in the URI never appears in Status, Info or errors).
- [ ] **Step 2:** Run `go test ./internal/app/ -v`. Expected: FAIL.
- [ ] **Step 3:** Implement, then regenerate bindings.
- [ ] **Step 4:** Run `wails3 build && go vet ./... && go test -race ./...`. Expected: PASS.
- [ ] **Step 5:** Commit `feat(app): sync controller, bindings and status events`.

---

### Task 8: Tray status dot and tooltip

**Files:**
- Create: `internal/badge/dot.go`, `internal/badge/dot_test.go`, `internal/app/synctext.go`, `internal/app/synctext_test.go`
- Modify: `internal/app/badge_wiring.go`, `internal/app/badge_wiring_test.go`, `internal/app/app.go`

**Interfaces:**
- Consumes: Task 7 `cloudsync.Status` via a `Sync func() cloudsync.Status` dep.
- Produces:
  ```go
  // badge
  type DotStyle int
  const (DotNone DotStyle = iota; DotSynced; DotSyncingA; DotSyncingB; DotOffline; DotError)
  var Green = color.NRGBA{0x16, 0xA3, 0x4A, 0xFF}; var Grey = color.NRGBA{0x64, 0x74, 0x8B, 0xFF} // Red exists
  func Dot(base image.Image, s DotStyle) image.Image         // top-right quarter; diameter 40% of the icon; glyph cleared 1 px around it
  func DotTemplate(base image.Image, s DotStyle) image.Image // alpha-only shapes for the macOS template icon
  // app
  func relTime(now, t time.Time) string                   // "just now" (<1 min), "N min ago", "N h ago" (<24 h), else "on 26 Nov"
  func syncTooltip(st cloudsync.Status, now time.Time) string // "" | "Syncing…" | "Synced · 2 min ago" | "Offline · 4 changes waiting" | "Offline" | "Sync error: <reason>"
  func dotFor(st cloudsync.Status, frame bool) badge.DotStyle
  func (w *badgeWiring) SyncChanged() // re-renders icons and tooltip without a store read
  ```
- Rules:
  - **Colored dots:** Synced = solid green, Offline = grey ring, Error = solid red, Syncing = alternating green ring and solid green every 500 ms (driven by a ticker that exists only while the state is `syncing`), off = no dot.
  - **macOS template shapes:** synced = filled dot, offline = ring, error = filled dot with a knocked-out horizontal bar, syncing alternates ring and filled. The tooltip carries the words. Record this in the UI guide as a shipped deviation: template images can't carry colour.
  - **Composition order:** base → pending badge (bottom-right) → dot (top-right).
  - **Tooltip:** `Awesome Reminder — N pending — <sync> — hotkey error: …`, skipping empty parts. Each status event re-renders it, so the "N min ago" text is never more than one poll (30 s) stale.
- [ ] **Step 1: Write failing tests:**
  - `TestDotPixels` (32 px: synced → the centre of the top-right quarter is green-dominant; offline → a ring pixel is grey and the dot centre is transparent; error → red; `DotNone` → base unchanged).
  - `TestDotTemplateAlphaOnly` (output RGB is black wherever alpha > 0).
  - `TestDotAndBadgeDontOverlap` (n=3 plus synced: bottom-right red, top-right green).
  - `TestRelTime` (0 s, 59 s, 61 s, 59 min, 3 h, 2 days).
  - `TestSyncTooltip` table (every state; "1 change waiting" singular; offline 0 → `Offline`).
  - `TestBadgeWiringSyncChanged` (fake tray and fake ticker: off → plain; syncing → two alternating icon sets over two ticks; synced → the ticker stops; the tooltip order matches).
- [ ] **Step 2:** Run `go test ./internal/badge/ ./internal/app/ -v`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `wails3 build && go test -race ./...`. Manual temp-copy run: connect, then pull the network → the grey ring and tooltip `Offline · N changes waiting`; restore the network → the green dot. Expected: as described.
- [ ] **Step 5:** Commit `feat(app): sync status dot and tooltip on the tray icon`.

---

### Task 9: List window — status pill, details popover, offline markers

**Files:**
- Create: `frontend/src/sync-status.ts`, `frontend/src/sync-status.test.ts`, `frontend/src/sync-pill.ts`
- Modify: `frontend/src/list.ts` (header slot before the settings button; refresh the view when `pending` changes), `frontend/src/item-row.ts` (marker), `frontend/src/icons.ts` (`Cloud`, `CloudCheck`, `CloudOff`, `CloudAlert`, `RefreshCw`; check the names in the installed `lucide`; as shipped, `cloudAlert` renders Lucide `CloudExclamationPoint` (`cloud-exclamation-point`), the installed version has no `cloud-alert`), `frontend/src/tokens.css`, `frontend/src/style.css`, `frontend/src/list-demo.ts` (a state switcher for every status)

**Interfaces:**
- Consumes: Task 7 `SyncStatus`, `SyncNow`, `OpenSettings`, `onSyncStatus`; `View.pending` (Task 3).
- Produces (`sync-status.ts`, pure):
  ```ts
  type Tone = "ok" | "neutral" | "muted" | "danger";
  type PillModel = { text: string; icon: "cloud" | "cloudCheck" | "cloudOff" | "cloudAlert" | "refresh"; tone: Tone; spin: boolean };
  function pillModel(st: SyncStatus, now: Date): PillModel | null; // null when state === "off"
  function relTime(now: Date, t: Date): string;                    // same wording as Go relTime
  function shouldGlow(prev: SyncStatus | null, next: SyncStatus): boolean; // prev.pending > 0 && next.pending === 0 && next.state === "synced"
  function showMarkers(st: SyncStatus): boolean;                   // state !== "off"
  ```
- Behavior:
  - **Pill:** a `button` with `aria-expanded` / `aria-controls`, pill radius, 12 px text.
    - Tones map to new tokens `--sync-ok-fg/bg`, `--sync-muted-fg/bg`, `--sync-danger-fg/bg`, each contrast-checked ≥ 4.5:1 in both themes.
    - `refresh-cw` spins only while syncing, and is static under reduced motion.
    - The relative time re-renders every 30 s while the window is open.
    - **Glow:** when `shouldGlow`, one `--sync-glow` box-shadow pulse over `--dur-sync-glow` (600 ms), on opacity of a `::after` layer; skipped under reduced motion.
  - **Popover:** opens below the pill; Esc or an outside click closes it; focus returns to the pill. It contains:
    - the state line;
    - `Last synced 10:42 (2 min ago)`;
    - `N changes waiting`;
    - the error reason in `--danger-text`;
    - each warning in `--time-text`;
    - `Cluster <host>`;
    - buttons `Sync now` (primary) and `Sync settings` (ghost → `OpenSettings`).
    `role="status"` announces state changes.
  - **Row marker:** a 14 px `cloud-off` icon after the text in `--fg-muted`, with `aria-label` and `title` `Not synced yet`. It shows when `showMarkers` is true and the id is in `view.pending`. When a refresh removes markers, they fade out over 180 ms (no fade under reduced motion), with no layout shift: the slot is reserved while markers are visible.
  - **On `sync:status`:** update the pill. If `pending` changed, refetch `View`.
- [ ] **Step 1: Write failing vitest tests** `sync-status.test.ts`:
  - `pillModel` rows:
    - off → null
    - syncing → `Syncing…` / refresh / spin
    - synced with pending 0 and last sync 10 s ago → `All synced · just now` / cloudCheck / ok
    - synced with pending 3 → `Synced · 3 waiting`
    - offline with pending 1 → `Offline · 1 change waiting` / cloudOff / muted
    - offline with pending 0 → `Offline`
    - error → `Sync error` / cloudAlert / danger
  - `relTime` rows matching the Go table.
  - `shouldGlow` (2→0 synced → true; 0→0 → false; 2→0 offline → false; null prev → false).
- [ ] **Step 2:** Run `npm --prefix frontend test`. Expected: FAIL.
- [ ] **Step 3:** Implement following the UI guide checklist.
- [ ] **Step 4:** Run `npm --prefix frontend test && npm --prefix frontend run typecheck && wails3 build`. In the demo harness (light and dark): every pill state, the popover by keyboard, markers appearing and fading, and the glow once on 2→0. Expected: as described, with no layout shift.
- [ ] **Step 5:** Commit `feat(ui): sync status pill, details and offline row markers`.

---

### Task 10: Settings › Sync section

**Files:**
- Create: `frontend/src/sync-settings.ts`, `frontend/src/sync-settings.test.ts`
- Modify: `frontend/src/settings.ts`, `frontend/src/style.css`, `frontend/src/tokens.css` (only if a new token is needed)

**Interfaces:**
- Consumes: Task 7 `SyncConnect`, `SyncDisconnect`, `SyncNow`, `SyncInfo`, `onSyncStatus`, `OpenURL`; Task 9 `pillModel`.
- Produces (`sync-settings.ts`, pure):
  - `looksLikeMongoURI(s: string): boolean` is true for a trimmed string starting with `mongodb+srv://` or `mongodb://`.
  - `storageLine(bytes: number): string` returns `Images in Atlas: 12.4 MB of 512 MB`, or `Images in Atlas: unknown` when bytes < 0. It uses decimal MB with one decimal place.
- Behavior: a "Sync across machines" section at the end of the form. It sits outside the Save/dirty flow, because its buttons act immediately.
  - **Not connected:**
    - a label `Atlas connection string`;
    - a password input with a show/hide toggle (`eye` icon, `aria-pressed`);
    - helper text: `Paste the mongodb+srv:// string from Atlas. Use a database user with readWrite on awesome_reminder only.` followed by a `Setup guide` link (`OpenURL("https://github.com/priyankatgit/awsome-reminder/blob/master/docs/atlas-setup.md")`);
    - a `Connect` button. Enter in the field triggers it; it's disabled until `looksLikeMongoURI`, and shows `Connecting…` while busy. The error appears inline in `--danger-text`.
  - **Connected:**
    - a status line (the `pillModel` text plus its icon);
    - `Cluster <host>`;
    - `Connection string stored in <secretStore>`, with the extra note `Encrypted with a key kept on this computer; anyone with access to your account can read it.` when the fallback is used;
    - the storage line (fetched via `SyncInfo` on open and after each successful sync);
    - each warning (clock skew, scope), if any;
    - buttons `Sync now` and `Disconnect`. Disconnect asks inline: `Disconnect this computer? Your items stay here and Atlas keeps its copy.` [Disconnect] [Cancel].
  - **Help text under the section:** `Free clusters can pause after a long time unused; resume it in Atlas if sync says it can't reach the cluster. Atlas must allow this computer's IP (0.0.0.0/0 for laptops that move between networks; the strong password protects it).`
- [ ] **Step 1: Write failing vitest tests:**
  - `looksLikeMongoURI` (srv, plain, leading spaces → true; `https://…`, `""` → false).
  - `storageLine` (12_400_000 → `Images in Atlas: 12.4 MB of 512 MB`; 0 → `0.0 MB`; -1 → unknown).
- [ ] **Step 2:** Run `npm --prefix frontend test`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `npm --prefix frontend test && npm --prefix frontend run typecheck && wails3 build && go test ./...`. Manual temp-copy run:
  - a bad URI → inline error, nothing saved;
  - a good URI → connected view with the host;
  - Disconnect → the input is back and items remain.
- [ ] **Step 5:** Commit `feat(settings): sync section with connect, status and disconnect`.

---

### Task 11: Docs, setup guide, checklist, measurements

**Files:**
- Create: `docs/atlas-setup.md`
- Modify: `README.md` (Sync section; the roadmap marks Phase 2 done), `docs/manual-test-checklist.md`, `docs/superpowers/specs/2026-11-27-ui-design-guide.md` (a "Sync status" section: pill, popover, marker, tray dot, the macOS template deviation, new tokens)

- [ ] **Step 1:** Write `docs/atlas-setup.md`:
  - create a free M0;
  - create a database user with **Specific Privileges → readWrite on `awesome_reminder`** only, because the cluster may host other projects (a broader user still syncs, but the app shows the scope warning);
  - use a strong generated password;
  - Network Access, with `0.0.0.0/0` for roaming laptops and why that is acceptable here;
  - copy the SRV string, paste it in Settings, and Connect;
  - restore on a new machine;
  - what syncs and what stays local (per the 2026-11-28 decision);
  - the clock-skew note;
  - pausing after inactivity;
  - storage (512 MB; images ≤ 600 KB each);
  - Disconnect.
- [ ] **Step 2:** Update the README and the UI guide to match the shipped behavior. Check every claim against the code.
- [ ] **Step 3:** Add checklist rows and run them on Windows from a temp copy, marking human-only rows "needs human":
  - two machines converge within 30 s;
  - an edit offline shows the marker, then syncs on reconnect and the glow plays;
  - restore on a fresh profile (items, images, lead times; no missed-card flood);
  - tray dot states and tooltips;
  - wrong password → `Atlas rejected the username or password.`;
  - a user with `readWriteAnyDatabase` → the scope warning in Settings and the pill popover, and sync still works;
  - paused cluster wording;
  - Done on machine A removes the alert card on machine B;
  - **idle RAM with sync on after 10 min < 150 MB** (record the numbers in the measurements section);
  - ~0% idle CPU with sync on.
- [ ] **Step 4:** Commit `docs: Phase 2 sync guide, README, checklist, design guide`.
