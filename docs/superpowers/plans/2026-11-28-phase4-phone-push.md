# Phase 4: Phone push through ntfy — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Optional phone push for reminders (and opt-in reply nudges / end-of-day sweep) through ntfy, scheduled ahead on the ntfy server so the phone buzzes with every computer off.

**Architecture:** A new `internal/push` package computes the desired set of scheduled ntfy messages from open items (via a new `scheduler.Upcoming`), diffs it against a local `push_log` table, and publishes (delete-then-publish, sequence ID in the path) or deletes. A control listener streams `<topic>-ctl` and applies phone **Done** taps through `core.DoneThrough`. Settings gain synced push fields; the optional access token lives in a second `internal/secret` store.

**Tech Stack:** Go 1.26 (go.mod), Wails v3.1.0, vanilla TS + vitest, SQLite (modernc), `net/http`, new dep `github.com/skip2/go-qrcode` (pure Go).

**Spec:** `docs/superpowers/specs/2026-11-28-phone-push-ntfy-design.md` — read it first; this plan does not repeat its rationale.

## Global Constraints

- Push is **off by default**; with it off, nothing touches the network and no goroutine runs beyond an idle reconciler waiting on its kick channel.
- **Always the path form:** publish `POST <server>/<topic>/<seq>`, cancel `DELETE <server>/<topic>/<seq>`. Never the `X-Sequence-ID` header (verified on ntfy.sh: header-form messages can't be cancelled).
- **Every publish is delete-then-publish** (ntfy.sh delivers both copies when the same seq is published twice).
- Window: deliver time in `(now+15s, now+71h]`. Delay header: `X-Delay: <unix seconds>`.
- Default server `https://ntfy.sh`; topic `ar-` + 26 lowercase base32 chars from `crypto/rand`; topic must match `^[-_A-Za-z0-9]{1,64}$`.
- Grace options: 0, 2, 5, 10 minutes; default 2.
- Priorities: 5 for High-priority items, 4 otherwise, 3 for the sweep. Tags `alarm_clock` (reminder), `speech_balloon` (nudge/sweep).
- Never log the token, the topic, or message bodies. Redact the topic in logged URLs as `<topic>`.
- Network never blocks capture, the scheduler loop, the alert window or sync. All push I/O runs on the push goroutines with timeouts (15 s per request).
- Quota guard: count publishes **and** deletes per local day; above 200 only publish pushes delivering within 6 h; status shows a warning.
- Wails bindings: after changing exported `Service` methods or bound types, regenerate with `wails3 generate bindings -clean=true -ts -i` (see `build/Taskfile.yml` `generate:bindings`) and commit `frontend/bindings`.
- Tests: `go test ./...` and `cd frontend && npx vitest run` must pass at the end of every task. Follow the existing style (table tests, fakes, `httptest`).
- Never run `bin/awesome-reminder.exe` from the repo path; manual runs use a temp copy with `APPDATA`/`LOCALAPPDATA` pointed at a temp dir, stopped promptly.
- Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **Settings synced from an older device** (no push fields in the Atlas doc) must not switch push off or blank the topic on a newer device → Task 1 test `TestApplyRemoteSharedKeepsPushFromOldDoc`.
2. **Recurring item marked done from the phone after the computer already advanced it** → the Done applies to the next occurrence only when `occ` matches the current or previous occurrence; a stale `occ` never completes a later occurrence → Task 7 tests.
3. **Server URL with trailing slash / subpath / uppercase scheme** (`https://ntfy.example.com/ntfy/`) builds correct URLs → Task 4 test `TestURLs`.
4. **Network flapping / laptop sleep mid-reconcile**: a failed delete or publish leaves the log unchanged so the next run retries, and never records a publish that failed → Task 6 tests.
5. **Task text with newlines, emoji, quotes, commas or semicolons** must not break the `X-Actions` header or the title header (headers can't hold newlines; non-ASCII must be RFC 2047-encoded or sent via JSON) → Task 5 test `TestMessageHeadersSafe`; the client publishes with the JSON body form if headers can't carry the title safely (builder's choice, but the test pins the outcome).

---

### Task 1: Synced push settings

**Files:**
- Modify: `internal/settings/settings.go` (fields, `Shared`, `WithShared`, `Equal`, wire JSON, `Defaults`, `Validate`)
- Create: `internal/settings/push.go` (`NewPushTopic`, `PushGraceOptions`)
- Modify: `internal/core/core.go` (`SaveSettings` fills an empty topic when enabling), `internal/core/sync.go` (`ApplyRemoteShared` older-doc fallback)
- Modify: `internal/cloudsync/atlas/docs.go`, `internal/cloudsync/memsync/memsync.go`, `internal/cloudsync/cloudsynctest/conformance.go` (round-trip the new fields)
- Test: `internal/settings/settings_test.go`, `internal/core/sync_test.go` (or the existing core sync test file), conformance

**Interfaces:**
- Produces: `Settings.PushEnabled bool`, `PushServer string`, `PushTopic string`, `PushReplies bool`, `PushHideText bool`, `PushGrace time.Duration`; JSON keys `pushEnabled, pushServer, pushTopic, pushReplies, pushHideText, pushGrace` (grace as a duration string like lead times, e.g. `"2m0s"`); `settings.Shared` gains the same six fields; `settings.NewPushTopic() string`; `settings.DefaultPushServer = "https://ntfy.sh"`.
- Atlas/memsync keys: `push_enabled, push_server, push_topic, push_replies, push_hide_text, push_grace_ms`.

- [ ] **Step 1: Write failing tests**
  - `TestDefaultsPush`: `Defaults("windows")` has `PushEnabled=false`, `PushServer="https://ntfy.sh"`, `PushTopic=""`, `PushGrace=2*time.Minute`.
  - `TestValidatePush` table: server `ftp://x` → error; `https://ntfy.sh/` ok (normalized to no trailing slash by core save); topic `"has space"` → error; topic 65 chars → error; grace `3*time.Minute` → error; `PushEnabled=true` with empty topic → error (core fills it first).
  - `TestNewPushTopic`: matches `^ar-[a-z2-7]{26}$`; two calls differ.
  - `TestUnmarshalOldSettingsPushDefaults`: JSON without push keys → push defaults (server, grace).
  - `TestSharedRoundTripPush`: `WithShared(s.Shared())` keeps push fields; `Equal` detects a changed topic.
  - `TestApplyRemoteSharedKeepsPushFromOldDoc`: local has push on + topic; remote newer `Shared` with `PushServer==""` → local push fields kept, other fields applied.
  - `TestCoreSaveSettingsFillsTopic`: saving `PushEnabled=true, PushTopic=""` stores a topic matching the pattern; server `"https://ntfy.sh/"` stored as `"https://ntfy.sh"`.
  - Conformance: extend the shared-settings round-trip at `conformance.go:307` with all six fields set.
- [ ] **Step 2: Run** `go test ./internal/settings/ ./internal/core/ ./internal/cloudsync/...` → FAIL.
- [ ] **Step 3: Implement.** A remote doc with `PushServer==""` is from an older device: keep the six local push fields (same pattern as the `EndOfDay` fallback in `ApplyRemoteShared`). Normalization (trim, drop trailing `/`, fill topic) happens in `Core.SaveSettings` before `Validate`.
- [ ] **Step 4: Run** the same tests → PASS; `go test ./...` → PASS.
- [ ] **Step 5: Commit** `feat(settings): synced phone push settings`.

### Task 2: Second secret for the ntfy token

**Files:**
- Modify: `internal/secret/secret.go`, `internal/secret/file.go`
- Test: `internal/secret/secret_test.go`

**Interfaces:**
- Produces: `secret.NewNamed(dataDir string, meta MetaStore, name Name) Store` with `secret.AtlasURI` (account `atlas-uri`, meta `sync_uri_enc`, key file `sync.key`, unchanged) and `secret.PushToken` (account `ntfy-token`, meta `push_token_enc`, key file `push.key`). `secret.New` stays and equals `NewNamed(..., AtlasURI)`. `ErrNotFound` unchanged.

- [ ] **Step 1: Failing tests** `TestNamedSecretsIndependent`: with the fake keychain, set both, delete Atlas → token still readable; same with the keychain failing (file fallback): deleting Atlas removes `sync.key` but `push.key` and token survive. Existing tests unchanged and passing.
- [ ] **Step 2: Run** `go test ./internal/secret/` → FAIL.
- [ ] **Step 3: Implement** by moving `account`, `metaKey`, `keyFile` into a `Name` struct held by `store`. Error text for the token: "couldn't remove the access token from the system keychain".
- [ ] **Step 4: Run** → PASS.
- [ ] **Step 5: Commit** `feat(secret): named secrets for the ntfy access token`.

### Task 3: `push_log` table and `scheduler.Upcoming`

**Files:**
- Modify: `internal/store/migrate.go` (v5), Create: `internal/store/pushlog.go`
- Modify: `internal/scheduler/plan.go` (or Create `internal/scheduler/upcoming.go`)
- Test: `internal/store/pushlog_test.go`, `internal/scheduler/upcoming_test.go`

**Interfaces:**
- Produces (store):
  ```go
  type PushEntry struct {
      Seq, Server, Topic, Hash string
      DeliverAt, SentAt     time.Time
      State                 string // "scheduled" | "dismissed"
  }
  func (s *Store) PushLog() ([]PushEntry, error)
  func (s *Store) PutPush(e PushEntry) error        // upsert by Seq
  func (s *Store) DeletePush(seq string) error
  func (s *Store) PrunePush(before time.Time) error // DeliverAt < before
  func (s *Store) ClearPush() error
  ```
  plus existing `Meta/SetMeta` for key `push_ctl_since`.
- Produces (scheduler):
  ```go
  const (OccReminder = "reminder"; OccNudge = "nudge"; OccSweep = "sweep")
  type Occurrence struct {
      Kind   string    // OccReminder, OccNudge, OccSweep
      ItemID string    // "" for a sweep
      At     time.Time // the occurrence (due time, snooze end, nudge time, sweep time); push adds grace
      Due    time.Time // the item's occurrence due time (zero for a reply without due / sweep)
      People []string  // sweep only
  }
  func Upcoming(items []model.Item, r Rules, from, to time.Time) []Occurrence // sorted by At, then ItemID
  ```
  Rules for `Upcoming`: an open item with `DueAt` yields its due time and, if recurring, each later occurrence (via `recur.Next` with the item's zone) with `At` in `[from, to]`; a `SnoozeUntil` later than the due time replaces that occurrence's `At` with the snooze end (Kind stays reminder; for an owed reply it is a nudge). An open owed reply yields its nudge times in the window (same rules as `nudges`, iterated forward). Sweeps: each working day's `EndOfDay` in the window when `r.Sweep` and there are open replies created before it. Done/deleted items yield nothing. Reuse the existing helpers; do not duplicate the nudge/sweep rules.

- [ ] **Step 1: Failing tests**
  - Store: `TestPushLogCRUD` (put, upsert changes hash, list, delete, prune by time, clear); migration test opens a v4 DB and reaches v5.
  - Scheduler `TestUpcoming` table (Loc = UTC, now Wed 2026-11-25 10:00):
    - reminder due 17:00 today → one `{reminder, At 17:00}`;
    - daily recurring 09:00, window 71h → Thu, Fri, Sat 09:00 (3);
    - due 17:00 snoozed to 18:00 → `At 18:00`, `Due 17:00`;
    - done item → none; todo without due → none;
    - reply created 10:00 no due, work days Mon–Fri, DefaultTime 09:00 → nudges 12:00 Wed, 09:00 Thu, 09:00 Fri (Sat 10:00 window end excludes Mon);
    - sweep on, EndOfDay 17:30, one open reply created 10:00 → sweeps Wed, Thu, Fri 17:30 with `People` = `["Priya"]`;
    - occurrence exactly at `from` included, at `to` included, after `to` excluded.
- [ ] **Step 2: Run** `go test ./internal/store/ ./internal/scheduler/` → FAIL.
- [ ] **Step 3: Implement.** v5 migration:
  ```sql
  CREATE TABLE push_log (seq TEXT PRIMARY KEY, server TEXT NOT NULL, topic TEXT NOT NULL,
    deliver_at INTEGER NOT NULL, hash TEXT NOT NULL, state TEXT NOT NULL, sent_at INTEGER NOT NULL);
  ```
- [ ] **Step 4: Run** → PASS; `go test ./...` → PASS.
- [ ] **Step 5: Commit** `feat: push log table and scheduler.Upcoming`.

### Task 4: ntfy client and fake server

**Files:**
- Create: `internal/push/ntfy.go`, `internal/push/pushtest/server.go`
- Test: `internal/push/ntfy_test.go`

**Interfaces:**
- Produces:
  ```go
  type Message struct {
      Seq       string
      DeliverAt time.Time
      Title, Body string
      Priority  int
      Tags      []string
      Actions   []Action
  }
  type Action struct { Label, URL, Method, Body string; Headers map[string]string; Clear bool } // all "http" actions
  type Client struct { Server, Topic, Token string; HTTP *http.Client }
  func (c Client) Schedule(ctx context.Context, m Message) error           // DELETE then POST, path form
  func (c Client) Delete(ctx context.Context, seq string) error            // 404 = nil
  func (c Client) SendNow(ctx context.Context, title, body string) error   // test notification, no delay, no seq
  func (c Client) PostControl(ctx context.Context, body []byte) error      // POST <server>/<topic>-ctl (tests/fake phone)
  func (c Client) PollControl(ctx context.Context, since string) ([]CtlMessage, error) // GET <topic>-ctl/json?poll=1&since=<since|all>
  func (c Client) StreamControl(ctx context.Context, fn func(CtlMessage)) error        // GET <topic>-ctl/json, returns on error/ctx
  type CtlMessage struct { ID, Message string }
  // Errors
  var ErrAuth = errors.New("ntfy: not authorized")   // 401/403
  var ErrRateLimited = errors.New("ntfy: rate limited") // 429
  type TempError struct{ Err error }                 // network errors and 5xx
  ```
  Requests carry `Authorization: Bearer <token>` when set and a 15 s timeout.
- `pushtest.Server` (an `httptest.Server`) models ntfy.sh as verified: path-form publish with `X-Delay` stores a pending message; `DELETE /<topic>/<seq>` removes pending messages with that seq; publishing the same seq twice keeps **both**; `Deliver(now)` returns and moves due messages to delivered; records all requests; can require a token and inject 401/429/500 per path; serves `-ctl` poll and stream (`{"id","event":"message","message"}` lines).

- [ ] **Step 1: Failing tests**
  - `TestScheduleDeleteThenPost`: Schedule twice the same seq with different bodies → fake has exactly one pending (the second); request log is `DELETE, POST, DELETE, POST`; POST path is `/<topic>/<seq>`; `X-Delay` = unix seconds; no `X-Sequence-ID` header.
  - `TestDeleteCancels` and `TestDelete404IsNil`.
  - `TestAuthAndErrors`: token sent as Bearer; 401 → `errors.Is(err, ErrAuth)`; 429 → `ErrRateLimited`; 500 and a closed server → `*TempError`.
  - `TestURLs`: server `HTTPS://ntfy.example.com/ntfy/` → requests to `/ntfy/<topic>/<seq>`.
  - `TestControlPollAndStream`: posted control bodies come back from Poll (`since=all` when empty) and Stream with their IDs.
- [ ] **Step 2: Run** `go test ./internal/push/...` → FAIL.
- [ ] **Step 3: Implement.** Send title/body/actions in the JSON publish form if needed for non-ASCII and newlines (ntfy accepts `POST <server>/` with a JSON body containing `topic`, `title`, `message`, `priority`, `tags`, `actions`, `delay`; check the JSON form also accepts the sequence ID in the path, otherwise use headers with RFC 2047 encoding) — the test in Task 5 pins the outcome either way.
- [ ] **Step 4: Run** → PASS.
- [ ] **Step 5: Commit** `feat(push): ntfy client with delete-then-publish and a fake ntfy server`.

### Task 5: Desired set and message building

**Files:**
- Create: `internal/push/desired.go`
- Test: `internal/push/desired_test.go`

**Interfaces:**
- Consumes: `scheduler.Upcoming`, `scheduler.Rules`, `settings.Settings`, `push.Message`.
- Produces:
  ```go
  type Config struct { Server, Topic, Token string; Replies, HideText bool; Grace time.Duration; Loc *time.Location }
  func ConfigFrom(s settings.Settings, token string, loc *time.Location) Config
  func SeqFor(itemID, kind string, occ time.Time) string   // "ar" + lower base32(sha256(itemID|kind|occ.Unix()))[:20] — whole SECONDS, so alert keys (unix seconds) map to the same seq; sweep: SeqFor("sweep", OccSweep, at)
  func Desired(items []model.Item, r scheduler.Rules, c Config, now time.Time) map[string]Message
  func Hash(m Message) string                              // sha256 of every field incl. DeliverAt
  ```
  `Desired` calls `Upcoming(items, r, now+15s-grace, now+71h-grace)`, drops nudges/sweeps unless `c.Replies`, sets `DeliverAt = occ.At + Grace`, and builds:
  - Title (reminder): `⏰ 17:00 · <text>`; when the local date isn't today: `⏰ Thu 26 Nov 17:00 · <text>`. Hidden: `Reminder due · 17:00`. Nudge: `Reply owed · <OwedTo or "Someone">`, hidden `Reply owed`. Sweep: `<n> people still waiting` (`1 person still waiting`).
  - Body: text (+ ` #tag` for each tag) truncated so the whole message stays under 3,500 bytes, ending in `…` when cut; nudge body `waiting <age> · <text>`; sweep body `Priya, Sam, Ops channel`. Hidden: `Open Awesome Reminder on a computer.`; hidden sweep `<n> replies owed`.
  - Priority 5 for `model.PriorityHigh`, else 4; sweep 3. Tags per Global Constraints.
  - Actions (reminder, nudge; none for sweep):
    - `Done`: POST `<server>/<topic>-ctl`, body `{"op":"done","id":"<itemID>","occ":<occ.Due (or occ.At when Due is zero) unix seconds>}`, `Clear: true`.
    - `Snooze 1h`: POST `<server>/<topic>/<seq>s`, headers `X-Delay: 1h`, `X-Title: Snoozed · <title>` (or the JSON form), body = the same body, `Clear: true`.
    - Both carry `Authorization: Bearer <token>` in `Headers` only when `c.Token != ""`.
  Times use the item's `TZ` when loadable, else `c.Loc`.

- [ ] **Step 1: Failing tests**
  - `TestSeqForStable`: same inputs → same 22-char `ar…` value; different occurrence → different.
  - `TestDesiredReminderGrace`: due 17:00, grace 2m, now 10:00 → one message, `DeliverAt` 17:02, title `⏰ 17:00 · check deploy`, priority 4, two actions with the exact URLs above.
  - `TestDesiredWindow`: due in 70h59m with grace 2m → excluded only if `DeliverAt > now+71h`; due in 10 s → excluded.
  - `TestDesiredHidden`: no task text anywhere in title, body or action headers.
  - `TestDesiredRepliesOptIn`: reply nudges and sweep only with `Replies=true`; sweep has no actions, priority 3.
  - `TestDesiredHighPriority` → 5.
  - `TestMessageHeadersSafe`: text `fix "a, b; c"\nnow 🚀` → Schedule against the fake server succeeds and the fake decodes title/body exactly (round-trip through `pushtest`).
  - `TestDesiredTokenInActions`: token set → both actions carry it; unset → no Authorization header.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** → PASS.
- [ ] **Step 5: Commit** `feat(push): desired set and message format`.

### Task 6: Reconciler

**Files:**
- Create: `internal/push/reconciler.go`
- Test: `internal/push/reconciler_test.go`

**Interfaces:**
- Consumes: Tasks 2–5.
- Produces:
  ```go
  type Log interface { // *store.Store satisfies it
      PushLog() ([]store.PushEntry, error); PutPush(store.PushEntry) error; DeletePush(string) error
      PrunePush(time.Time) error; ClearPush() error
  }
  type Deps struct {
      Items  func() ([]model.Item, error)          // open items
      Rules  func() scheduler.Rules
      Config func() (Config, bool)                  // false = push off
      Log    Log
      Now    func() time.Time
      HTTP   *http.Client
      OnStatus func(Status)
  }
  type Status struct {
      State     string    `json:"state"`     // "off" | "ok" | "offline" | "auth" | "quota" | "error"
      Scheduled int       `json:"scheduled"` // entries pending in the log
      LastSent  time.Time `json:"lastSent"`
      Message   string    `json:"message"`   // user-facing line, e.g. "Error: 401 — this computer needs an access token"
      SentToday int       `json:"sentToday"`
  }
  type Reconciler struct{ /* … */ }
  func NewReconciler(d Deps) *Reconciler
  func (r *Reconciler) Start(ctx context.Context)
  func (r *Reconciler) Wait()
  func (r *Reconciler) Kick()                          // coalescing, never blocks
  func (r *Reconciler) Dismiss(seqs []string)          // async: DELETE seq and seq+"s", mark log "dismissed"
  func (r *Reconciler) Status() Status
  func (r *Reconciler) SendTest(ctx context.Context) error
  ```
  Behaviour (spec "Reconciler"): run on Start, Kick, every 15 min, after a wall-clock jump > 5 s vs monotonic (wake), and on retry timers. Each run: compute `Desired`; for each desired seq not in the log, or with a different hash, `Schedule` then `PutPush(state=scheduled)`; skip seqs whose log state is `dismissed`; for each `scheduled` log entry not desired and with `DeliverAt` in the future, `Delete(seq)` and `Delete(seq+"s")`, then `DeletePush`; `PrunePush(now-1h)`. If the config's server/topic differs from a log entry's, delete it on its own server/topic (best effort, with that entry's token = current token) and drop it. Push off → delete all pending entries, `ClearPush`, status `off`. Errors: `TempError` → stop this run, retry after backoff 30 s doubling to 15 min, status `offline`; `ErrAuth` → status `auth`, no retry until the next Kick; `ErrRateLimited` → wait for the 15-min tick. Quota: count per local day (in memory, reset at local midnight); above 200 only schedule `DeliverAt <= now+6h`, status `quota`.

- [ ] **Step 1: Failing tests** (fake server from Task 4, fake clock, in-memory Log):
  - `TestReconcileSchedulesOnce`: two runs with no change → one Schedule (`DELETE`,`POST` once).
  - `TestReconcileEditReplaces`: text edit → delete-then-post with new body; fake has one pending.
  - `TestReconcileDoneCancels`: item done → `DELETE seq` and `DELETE seqs`; fake has none pending; log empty.
  - `TestTwoComputersOneDelivery`: two reconcilers with separate logs, same fake server → after both run, `Deliver(due+grace)` returns exactly one message.
  - `TestDismissNotRepublished`: `Dismiss([seq])` → deleted; next run doesn't republish.
  - `TestTempErrorRetries`: fake returns 500 on POST once → log unchanged, status offline, a later retry schedules it.
  - `TestAuthStops`: 401 → status `auth`, no further requests until Kick.
  - `TestTopicChangeMovesPushes`: change topic → old topic's pending deleted, new topic has the message.
  - `TestTurnOffClears`: config off → all pending deleted, log cleared, status off.
  - `TestQuotaGuard`: after 200 requests, a push 10 h out isn't scheduled, one 2 h out is; status `quota`.
  - `TestKickNeverBlocks` and `TestWaitAfterCancel`.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** `go test -race ./internal/push/` → PASS.
- [ ] **Step 5: Commit** `feat(push): reconciler`.

### Task 7: Control listener (phone Done)

**Files:**
- Create: `internal/push/control.go`
- Test: `internal/push/control_test.go`

**Interfaces:**
- Produces:
  ```go
  type ControlDeps struct {
      Config func() (Config, bool)
      Meta   interface{ Meta(string) (string, bool, error); SetMeta(string, string) error } // key "push_ctl_since"
      Done   func(itemID string, occ time.Time) error // app: applies through core (see below)
      HTTP   *http.Client
  }
  func StartControl(ctx context.Context, d ControlDeps, restart <-chan struct{}) (wait func())
  func ApplyDone(c *core.Core, itemID string, occ time.Time) error
  ```
  Loop while config is on: `PollControl(since)` and apply each message, store the last ID, then `StreamControl` applying each; on error back off 5 s doubling to 5 min; on `restart` (config changed) reconnect. Bodies over 1 KiB, invalid JSON, `op != "done"` or empty id are ignored.
  `ApplyDone`: load the item; ignore (nil error) if it is missing or not open. If the item has a due time, apply `c.DoneThrough(id, occ)` only when `occ.Unix() == it.DueAt.Unix()`; otherwise ignore — a different occurrence means the computer already moved the item on (recurring advance, edit, snooze to a new due) and the phone tap is stale. A reply without a due time accepts any `occ`. Say this in a comment.

- [ ] **Step 1: Failing tests**
  - `TestApplyDoneCurrentOccurrence`: non-recurring due 17:00, occ 17:00 → done.
  - `TestApplyDoneStaleOccurrence`: recurring item already advanced to tomorrow 09:00, occ today 09:00 → unchanged.
  - `TestApplyDoneRecurringCurrent`: daily item due today 09:00, occ 09:00 → advanced to tomorrow 09:00, still open.
  - `TestApplyDoneReplyNoDue` → reply marked replied.
  - `TestApplyDoneDoneItem` → no error, unchanged.
  - `TestControlPollsThenStreams`: messages posted while stopped are applied on start (poll since stored id), then a newly posted one is applied via stream; `push_ctl_since` advances.
  - `TestControlIgnoresGarbage`: oversize, invalid JSON, `op:"delete"` → Done never called.
  - `TestControlRestartOnConfigChange`: switching topic reconnects to the new `-ctl`.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** `go test -race ./internal/push/` → PASS.
- [ ] **Step 5: Commit** `feat(push): control topic listener for phone Done`.

### Task 8: App wiring and bindings

**Files:**
- Create: `internal/app/push.go`
- Modify: `internal/app/app.go` (construct, Start in `ApplicationStarted`, stop/Wait in the shutdown defer before the store closes; kick in `c.OnChange` and after `remoteApplied`), `internal/app/alerts.go` (dismiss hook), `internal/app/service.go` (`Deps.Push`), `internal/app/events.go` (`EventPushStatus = "push:status"`)
- Modify: `go.mod`/`go.sum` (`github.com/skip2/go-qrcode`)
- Test: `internal/app/push_test.go`, `internal/app/alerts_test.go`

**Interfaces:**
- Consumes: Tasks 1–7.
- Produces bindings on `Service`:
  ```go
  type PushInfo struct {
      Status     push.Status `json:"status"`
      HasToken   bool        `json:"hasToken"`
      TokenStore string      `json:"tokenStore"` // secret.Store.Kind()
      SubscribeURL string    `json:"subscribeUrl"` // <server>/<topic>, "" when no topic
      QR         string      `json:"qr"`           // "data:image/png;base64,…" of SubscribeURL, "" when none
  }
  func (s *Service) PushInfo() PushInfo
  func (s *Service) PushSetToken(token string) error   // trims; "" rejected with "Paste an access token"
  func (s *Service) PushForgetToken() error
  func (s *Service) PushTest() error                    // SendNow("Awesome Reminder", "Test notification — phone push works.")
  func (s *Service) PushNewTopic() (settings.Settings, error) // saves a new topic via Core.SaveSettings, returns the settings
  ```
  Dismiss hook: `DismissAlert(key)` and `DismissAlerts()` compute, for each removed card that counts (spec "Grace": `Lead == 0` — at-time, missed, snooze-end, nudge, sweep; not lead > 0), the seq: reminder `push.SeqFor(itemID, OccReminder, occ)` where occ is the card's `DueAt` for an at-time/missed card and the snooze end for a snooze card (parse from the key `<id>|snooze|<unix>`); nudge `SeqFor(itemID, OccNudge, card.DueAt)` (a nudge card's `DueAt` is its own time; a snoozed reply's snooze-end card is a nudge with key `<id>|snooze|<unix>` — use the snooze end); sweep `SeqFor("sweep", OccSweep, card.DueAt)`; then `Reconciler.Dismiss(seqs)`. The occurrence time passed to `SeqFor` must be the same `Occurrence.At` that `Upcoming` produced for it — test this pairing.

- [ ] **Step 1: Failing tests**
  - `TestDismissSeqMatchesUpcoming`: for an at-time card, a snooze-end card, a nudge card and a sweep card, the seq computed by the dismiss hook equals `SeqFor` of the matching `Upcoming` occurrence.
  - `TestDismissLeadCardKeepsPush`: dismissing a `Lead=10m` card calls `Dismiss` with nothing.
  - `TestDismissAllAppliesRule`: queue with one lead card and one at-time card → only the at-time seq.
  - `TestPushSetTokenRejectsEmpty`; `TestPushInfoQR`: with a topic, `QR` starts with `data:image/png;base64,` and `SubscribeURL` is `https://ntfy.sh/<topic>`.
  - `TestPushNewTopicChangesTopic`.
- [ ] **Step 2: Run** `go test ./internal/app/` → FAIL.
- [ ] **Step 3: Implement.** The reconciler's `Config` reads `c.Settings()` and the token store each run; token/settings changes `Kick()` it and signal the control listener's `restart`. Status changes `Emit(EventPushStatus, status)`. The control listener's `Done` is `push.ApplyDone(c, …)` then `svc.dropItemAlerts(id)`. Regenerate bindings.
- [ ] **Step 4: Run** `go test ./...` → PASS; `go build ./...` → OK.
- [ ] **Step 5: Commit** `feat(app): wire phone push, bindings and dismiss hook`.

### Task 9: Settings › Phone push section

**Files:**
- Create: `frontend/src/push-settings.ts`, `frontend/src/push-settings.test.ts`
- Modify: `frontend/src/settings.ts` (mount the section; push fields join the existing Save/dirty form for the synced settings), `frontend/src/api.ts` (types, bindings, `onPushStatus`), `frontend/src/style.css` (only what the UI guide doesn't already cover)

**Interfaces:**
- Consumes: Task 8 bindings and `Settings` push fields.
- Produces: `mountPushSection(host: HTMLElement, form: …)`; pure helpers `pushStatusLine(s: PushStatus): string`, `graceLabel(ms: number): string`.

Layout (follow `docs/superpowers/specs/2026-11-27-ui-design-guide.md` and mirror `sync-settings.ts`):
```
Phone push                                         [ off ● ]
  Get reminders on your phone through ntfy, even with this computer closed.
  Server   [https://ntfy.sh                    ]
  Topic    [ar-k3j…                ] [Copy] [New topic]
           [QR]  Scan with the ntfy app, or subscribe to <server>/<topic>.
  Access token (this computer)  [••••••••] [Save] [Forget]    stored in: Windows Credential Manager
  [ ] Also push reply nudges and the end-of-day sweep
  [ ] Hide task text on the phone
  Buzz the phone if not handled here within  [2 minutes ▾]  (immediately, 2, 5, 10 minutes)
  [Send test]   Phone push: 4 scheduled · last sent 2 min ago
  Help: The server can read what is sent unless text is hidden. The topic works like a password.
        Alerts are only scheduled while a computer is on in the 3 days before. Self-hosted: ntfy 2.16 or newer.
        A token you save is included in the phone's Done/Snooze buttons; use one that can only post to this topic.
```
Turning the switch on for the first time shows the inline note `Reminder text will be sent to <host>.` with **Turn on** / **Cancel** (no note when Hide text is already on).

- [ ] **Step 1: Failing tests** (`vitest`): `pushStatusLine` for each state (`off` → `Phone push is off`, `ok` with 4 and lastSent 2 min ago → `Phone push: 4 scheduled · last sent 2 min ago`, `auth` → the status `message`, `offline` → `Offline · will retry`, `quota` → message); `graceLabel(0)` → `immediately`, `graceLabel(120000)` → `2 minutes`; mounting with push off hides the details; enabling shows the consent note.
- [ ] **Step 2: Run** `cd frontend && npx vitest run src/push-settings.test.ts` → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `npx vitest run` and `npx tsc --noEmit` → PASS.
- [ ] **Step 5: Commit** `feat(settings): Phone push section`.

### Task 10: Docs and final verification

**Files:**
- Modify: `README.md` (new "Phone push (optional)" section after "Sync across machines"; Settings list line; "Where data lives" mentions `push.key`; Status and roadmap: Phase 4 done, nothing left on the list), `docs/manual-test-checklist.md` (phone push section)

- [ ] **Step 1: README section** covering: what it does, setup (turn on, scan QR in the ntfy app, Send test), what gets pushed and the grace, Done/Snooze buttons (Done reaches the computers when one is online within 12 h; Snooze works with the computers off), privacy (server reads text unless hidden; topic is the password; token in buttons), limits (3 days ahead, a computer must be online in that window; ntfy.sh 250 messages/day; self-hosted ≥ 2.16), token storage.
- [ ] **Step 2: Checklist entries:** phone buzzes with the laptop closed (lid shut before the due time); two computers → one buzz; Done on the computer before due+grace → no buzz; dismiss at-time card within grace → no buzz; phone Done marks done on the computer; phone Snooze 1h re-buzzes with the laptop off; hidden text shows no text; token-protected self-hosted topic works.
- [ ] **Step 3: Manual end-to-end against real ntfy.sh** (controller does this, not a subagent): build, copy the exe to a temp dir, set `APPDATA`/`LOCALAPPDATA` to a temp dir, turn push on, subscribe to the topic with `curl -N <server>/<topic>/json` in the background, create `in 1 min test push` with grace 0 → message delivered once at the due time; create another and mark it done before due → not delivered; post a done to `<topic>-ctl` with curl for a third → it shows done in the app. Stop the app.
- [ ] **Step 4: Run** `go test ./...`, `go vet ./...`, `cd frontend && npx vitest run && npx tsc --noEmit` → all PASS.
- [ ] **Step 5: Commit** `docs: Phase 4 phone push`.
