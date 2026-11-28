# Phase 4: Phone push through ntfy — design

Status: draft for approval. Date: 2026-11-28. The user handed the design calls to me ("take your decision"); every decision below is *[my call]* unless it was already in the original spec (`2026-11-27-awesome-reminder-design.md`, "ntfy without duplicates").

## Intent

The user juggles many things and is often away from the desk. Desktop alerts only help while they are at the computer. Phase 4 makes reminders reach their phone through [ntfy](https://ntfy.sh) (a free, open-source push app with no sign-in), using ntfy.sh or a self-hosted ntfy server. It must work **when the laptop is closed, asleep or off**, which is the case that matters most.

## Goals

1. Optional and off by default. Nothing goes to ntfy until the user turns it on.
2. The phone buzzes for a due reminder even if every computer is asleep or off, provided some computer running the app was online at some point in the 3 days before.
3. One buzz per alert even with several synced computers running.
4. Done, snooze, edit or delete on any computer cancels or moves the pending phone buzz.
5. **Done** and **Snooze 1 h** buttons on the phone notification.
6. Push failures never delay or block desktop alerts, capture or sync.
7. Easy setup: turn on, scan a QR code with the phone, press **Send test**.

## Non-goals

- Adding tasks from the phone (separate roadmap item: phone input).
- End-to-end encryption (ntfy has none). The server can read what we send; we offer a "hide text" switch instead.
- A phone buzz for every lead-time alert. The phone gets one buzz per occurrence (see "What gets pushed").
- Telling the user on the phone that the setup broke (the computer shows it).
- Email, phone-call or other ntfy delivery channels.

## Anti-goals (failure even if it "works")

- Double buzzes from two computers.
- A phone buzz for something already done, snoozed or deleted on a computer.
- The phone buzzing while the user is at the computer and has already handled the alert there.
- Burning through ntfy.sh's 250 messages/day free quota through repeated re-publishing.
- Leaking the access token or the topic into logs.

## How ntfy is used (facts this design relies on)

From docs.ntfy.sh (checked 2026-11-28):

- `POST <server>/<topic>` publishes. Headers: `X-Title`, `X-Priority` (1–5), `X-Tags`, `X-Delay` (Unix timestamp; min 10 s, max 3 days on ntfy.sh), `X-Actions`, `X-Sequence-ID`, `Authorization: Bearer tk_…`.
- **Sequence IDs** (server v2.16.0+): `POST <server>/<topic>/<seq>` publishes with a sequence ID; `DELETE <server>/<topic>/<seq>` cancels a scheduled message before delivery (and removes a delivered one from clients).
- **Verified on ntfy.sh, 2026-11-28** (three throwaway-topic tests):
  - Published with the sequence ID **in the path**, a `DELETE` before delivery **cancels** it. Published with the `X-Sequence-ID` **header**, the `DELETE` does **not** stop delivery. → Always use the path form.
  - Publishing twice with the same sequence ID **delivers both** (the documented replace does not happen on ntfy.sh).
  - `DELETE` then publish again with the same sequence ID delivers **only the new one**. → "Replace" is implemented as delete-then-publish.
- Up to 3 action buttons; an `http` action sends a request from the phone (`http, <label>, <url>, method=…, headers.<h>=…, body=…, clear=true`).
- `GET <server>/<topic>/json?poll=1&since=<id>` lists cached messages (ntfy.sh caches 12 h); `GET <server>/<topic>/json` streams new ones.
- ntfy.sh free tier: 250 messages/day, 4,096-byte messages.

The plan's final manual check repeats the cancel and delete-then-publish behaviour from the built app against ntfy.sh.

## Approaches considered

| | Approach | Trade-off |
|---|---|---|
| A | **Push when the desktop alert fires.** The computer sends an immediate ntfy message as the alert shows. | Simplest, but silent when the laptop is closed or off, which is the main case. Rejected. |
| B | **Schedule ahead on the ntfy server** with `X-Delay` up to 3 days, with deterministic sequence IDs to replace/cancel. **Chosen.** | Works with every computer off. Needs reconciling a desired set of scheduled messages against what was sent. Limited to 3 days ahead, so far-future alerts are scheduled once they enter the window. |
| C | **Own cloud relay** (e.g. a Cloudflare Worker) holding all schedules. | No 3-day limit, but a server for the user to deploy and run. Out of proportion for this phase. |

## Design

### Settings (Settings › Phone push)

Synced (added to the shared settings group, so every computer schedules the same pushes):

| Setting | Default | Notes |
|---|---|---|
| `PushEnabled` | off | |
| `PushServer` | `https://ntfy.sh` | Any http(s) URL; self-hosted needs ntfy ≥ 2.16 (stated in help text). Trailing slash trimmed. |
| `PushTopic` | generated on first enable | `ar-` + 26 random base32 chars (130 bits) from `crypto/rand`. **New topic** button rotates it. |
| `PushReplies` | off | Also push reply-owed nudges and the end-of-day sweep. |
| `PushHideText` | off | Phone shows "Reminder due" / "Reply owed" with no task text. |
| `PushGrace` | 2 min | "Buzz the phone if not handled on a computer within": 0, 2, 5, 10 min. |

Per computer (never synced):

- **Access token** (optional; needed for protected topics or self-hosted servers with auth). Stored with `internal/secret` (OS keychain, encrypted-file fallback) as a second secret beside the Atlas URI. Never logged; shown masked in Settings with Save/Forget.
- The push log (below).

The group shows: the switch; server; topic with Copy and a **QR code** of `<server>/<topic>` (the ntfy Android/iOS apps and web app subscribe from that link); token field; the two checkboxes; the grace select; **Send test**; and a status line (`Phone push: 4 scheduled · last sent 2 min ago`, `Error: 401 — this computer needs an access token`, `Offline · will retry`). The help text states: the server can read what is sent (unless hidden), the topic is the password, and alerts are only scheduled if a computer is online in the 3 days before. Turning it on shows a one-line consent note: "Reminder text will be sent to <server host>."

QR generation: a small pure-Go QR library (e.g. `github.com/skip2/go-qrcode`) rendering a PNG data URL for the Settings page. *[builder may choose another pure-Go lib.]*

### What gets pushed

One push per **occurrence**, not per lead-time alert:

- **Reminder** (open, has `DueAt`): at the due time (or at `SnoozeUntil` if snoozed later than that), plus `PushGrace`. Recurring reminders: each occurrence inside the window (use `internal/recur` / the planner's occurrence logic).
- **Reply nudge** (only with `PushReplies`): at each nudge time the scheduler's `nudges()` computes, plus grace.
- **End-of-day sweep** (only with `PushReplies` and `EndOfDaySweep`): at each sweep time in the window, plus grace. Body lists the people (or "N replies owed" when hidden).
- To-dos without a due time are never pushed.

Only pushes whose delivery time is between `now + 15 s` and `now + 71 h` are scheduled (71 h leaves margin under the 3-day maximum). Each running computer re-evaluates (below), so later alerts get scheduled as they enter the window.

### Message format

- **Title:** `⏰ 17:00 · check deploy` (the local due time; date prefix when not today, e.g. `Thu 26 Nov 17:00`). Hidden: `Reminder due · 17:00`. Nudge: `Reply owed · Priya`; hidden `Reply owed`. Sweep: `3 people still waiting`.
- **Body:** the task text (truncated to keep the message under 4,096 bytes), tags as `#tag`; for a nudge "waiting 2 h". Hidden: "Open Awesome Reminder on a computer."
- **Priority:** 5 (urgent) for High-priority items, 4 (high) otherwise; 3 for the sweep.
- **Tags:** `alarm_clock` for reminders, `speech_balloon` for replies.
- **Delay:** `X-Delay: <unix seconds>`.
- **Sequence ID:** deterministic, see below.
- **Actions** (reminders and nudges; not the sweep): `Done` and `Snooze 1h` (see Phone actions).

### Deterministic sequence IDs → no duplicates without claims

`seq = "ar" + base32(sha256(itemID + "|" + kind + "|" + occurrenceUnixMs))[:20]` (sweep uses `"sweep"` and the sweep time). Every computer derives the same ID for the same occurrence. Every publish is **delete-then-publish** (`DELETE /<topic>/<seq>` then `POST /<topic>/<seq>`), so when a second computer schedules the same occurrence it cancels the first computer's copy: one buzz. (A simultaneous race between two computers within the same second could still double; accepted.) This replaces the original spec's Atlas claim (`ntfy.claimed_by`), so no item schema change and no sync dependency; it also works without sync.

The content hash is part of what we compare locally (below) but not of the sequence ID, so an edit to the text replaces in place.

### Reconciler

A `push` package component, run in its own goroutine:

- **Triggers:** start, item changes (local or pulled by sync — the same hooks that kick the scheduler), settings changes, wake from sleep, every 15 min, and retry timers.
- **Desired set:** from open items + rules + push settings, compute `{seq → message}` in the window.
- **Push log** (local SQLite table `push_log`: `seq, topic, server, deliver_at, content_hash, state (scheduled|deleted), sent_at`): what this computer last published.
- **Diff:** (each publish is delete-then-publish, path form) publish entries that are new or whose `deliver_at`/hash changed; `DELETE` entries in the log that are no longer desired and still in the future; forget log entries whose delivery time has passed. Delete 404 is success.
- Only this computer's log is consulted, so with N computers each occurrence is published ≤ N times (each delete-then-publish cancels the previous copy: one buzz); count deletes against the quota too — acceptable for quota.
- **Server/topic change** (or New topic): delete everything pending on the old topic (best effort), then publish to the new one.
- **Turned off:** delete everything pending, clear the log.
- **Failures:** network errors and 5xx retry with backoff (30 s → 15 min), the log only records successes; 401/403 stop and show "needs an access token" until settings or the token change; 429 backs off to the next 15-min tick. Nothing here touches the scheduler or the alert window.
- **Quota guard:** count publishes per local day; above 200, only publish pushes due in the next 6 h and show a warning in the status line.

### Grace: don't buzz when handled at the computer

The desktop alert fires at the due time; the phone push is scheduled for due + grace (default 2 min). Handling the alert on a computer within the grace cancels the push:

- **Done / Snooze / edit / delete** (any computer): the item changes, the reconciler on each computer drops the old push (delete by sequence ID — any computer can, since IDs are deterministic).
- **Dismiss (×)** on a card: dismiss is local and does not sync, so the computer that showed the card deletes that occurrence's push directly (`DELETE` by seq, plus `<seq>s`, and records the seq locally in `push_log` as `dismissed` so the reconciler does not republish it). *[my call]* Only cards at or after the occurrence count: the at-time card, a missed card, a snooze-end card, a nudge card and the sweep card. Dismissing a **heads-up** card (lead time > 0) does **not** cancel the phone buzz — seeing "in 15 min" is not handling it. **Dismiss all** applies the same rule to every card in the queue. The record is keyed by seq (occurrence), not by alert key.
- **Ignored card:** the phone buzzes after the grace, which is wanted.

With grace 0 the phone buzzes at the same moment as the desktop.

### Phone actions (Done / Snooze 1 h)

The phone cannot reach the laptop, so actions go through ntfy itself:

- **Done:** `http` action → `POST <server>/<topic>-ctl` with body `{"op":"done","id":"<itemID>","occ":<occurrenceUnixMs>}`, `clear=true`, plus `headers.Authorization=Bearer …` when a token is set. The phone does not subscribe to `-ctl`, so it never buzzes.
- **Snooze 1h:** `http` action → `POST <server>/<topic>/<seq>s` (path form, so computers can cancel it) with `headers.X-Delay=1h`, the same title/body (prefixed "Snoozed · ") and `clear=true`. This schedules the re-buzz on the server with every computer off. One action can send only one request, so the computers are not told about a phone snooze and the item is not snoozed on them. Instead, every computer that deletes a push for an occurrence (done, edit, delete, dismiss) also deletes `<seq>s`, so handling the item on a computer within the hour cancels the snoozed re-buzz too. The snoozed re-buzz carries only Done (no second snooze).
- **Control listener:** each computer with push on keeps a streaming `GET <server>/<topic>-ctl/json` subscription (reconnect with backoff; on (re)connect it first polls `?poll=1&since=<last id>` stored locally, so messages sent while it was off are applied when it comes back within ntfy's 12 h cache). For `done`: if the item is open and its current occurrence equals `occ`, mark it done through the normal core path (which syncs to other computers and lets recurring items advance). Otherwise ignore (stale or already handled). Messages are idempotent across computers.
- **Token in the buttons** *[my call]*: an `http` action runs on the phone, so on a protected topic it must carry a token. When the publishing computer has a token, it is put in the Done/Snooze actions (`headers.Authorization`), which means it is stored on the ntfy server and on every subscribed client. The Settings help says so and recommends a dedicated token that can only write to `<topic>` and `<topic>-ctl`. With no token (the ntfy.sh default, where the unguessable topic is the secret) nothing extra is exposed. The token is never logged.
- **Which occurrence a phone Done applies to** *[builder's call, accepted]*: the item's current due time, or the next occurrence after it (a recurring item may already have advanced); apply through `core.DoneThrough`. Control messages are applied before missed alerts are shown on wake.
- `push_log` keeps entries for 1 h after delivery so a later done/dismiss can still delete the phone-snoozed `<seq>s`.
- The topic is editable (needed to set up a second computer without sync); titles use the item's TZ, falling back to local.
- Anyone who knows the topic can post a done for an item ID — the same trust as the topic itself. Bodies are size-capped and validated; unknown ops are ignored.

### Wiring

- New package `internal/push`: ntfy client (publish, delete, poll/stream), the desired-set computation (pure, unit-tested; reuses scheduler occurrence/nudge/sweep logic — expose what is needed from `internal/scheduler` rather than duplicating it), reconciler, control listener.
- `internal/store`: migration for `push_log` and a meta key for the control-topic `since` id.
- `internal/settings`: new shared fields with defaults and JSON; included in `Shared` and its sync stamping.
- `internal/secret`: support a second named secret (ntfy token) without disturbing the Atlas URI secret.
- `internal/app`: create/start/stop the push service with the app lifecycle; kick it wherever the scheduler is kicked; card dismiss calls push delete; Settings bindings (save settings, set/forget token, send test, new topic, status); status events to the Settings window.
- Frontend: the Settings › Phone push group following the UI design guide.
- README section, manual test checklist entries (phone buzzes with laptop closed; two computers → one buzz; Done from phone marks done on computer).

### Logging

Log publish/delete/listener events at the existing sync logging level, with the topic and token redacted (reuse `cloudsync.Redact` style). Never log message bodies.

### Fallback

If a self-hosted server is older than v2.16 (deletes are ignored), pushes still arrive but cancel does not; Settings help states the version requirement. No Atlas claim is needed.

## Constraints

- Go 1.27 + Wails v3.1.0, vanilla TS frontend; follow the UI design guide.
- Idle cost: one goroutine and one long-lived HTTP stream when on; nothing at all when off.
- Never block capture, the alert window, the scheduler or sync on the network.
- Keep the existing test style (fakes, `httptest` ntfy server). The fake ntfy server must model what ntfy.sh actually does (scheduled delivery; path-form DELETE cancels a pending message; two publishes with the same sequence ID both deliver), so tests can assert "one delivery" across two simulated computers.
- Manual testing uses temp copies of the binary with `APPDATA` pointed at a temp dir, never `bin/awesome-reminder.exe` from the repo path.

## Left to the builder

- Exact Settings layout and copy within the UI guide.
- QR library choice (pure Go).
- How to share occurrence/nudge/sweep computation with the scheduler (exported helpers vs. a shared function).
- Backoff constants within the ranges above.
