# Task details panel, priority, live reminder indicator — Design (Phase 1.6)

Builds on `2026-11-27-awesome-reminder-design.md` (Phase 1 local core) and `2026-11-27-ui-design-guide.md`. Lands **after Phase 1.5 (voice dictation) is merged**, because both change the capture box; Phase 2 (Atlas sync) then carries the new fields from day one.

## Intent / why
The user (a developer) wants the app to stay a fast one-line capture tool **and** hold real detail when a task needs it — notes, steps, screenshots, importance — without becoming Todoist/Asana. They also want to *see while typing* whether the capture box understood a reminder.

## Goals
1. **Detail panel**: clicking a task in the list window slides a panel in from the right covering half the window; it edits title, priority, due/repeat, tags, and a Markdown description with a helper toolbar, inline images (paste/drag), and preview.
2. **Priority** (High / Medium / Low / none): set from the capture box via chips, keyboard, or typed token; shown and sorted in the list; editable in the panel.
3. **Live reminder indicator** in the capture box: an eye-catching light-red bell + "Reminder · <time>" badge that appears as soon as a time is detected and disappears when the time words are removed.

## Non-goals
Separate subtask entities (Markdown checklists cover it); WYSIWYG rich text; non-image attachments; sharing/collaboration; editing images; priority-based notifications.

## Anti-goals (failure even if it works)
- Capture gets slower or noisier (chips must not steal focus or add steps).
- Memory grows noticeably: no new windows; Markdown libs load only when the panel first opens.
- Images bloat Atlas: every image is compressed before storage.
- Untrusted HTML in descriptions can run script or navigate the webview.

## Approaches considered (user picked A + 1)
| | Option | Trade-off |
|---|---|---|
| **A ✅** | Drawer inside the list window (same page) | No extra WebView (~40–60 MB saved), instant, natural keyboard focus. |
| B | Separate docked detail window | Extra WebView memory, window alignment fiddly — rejected (RAM rule). |
| **1 ✅** | `<textarea>` + helper toolbar + `marked` + `DOMPurify` preview (~60 KB, lazy-loaded) | Light, fast, enough for notes/checklists/code/images. |
| 2 | CodeMirror 6 | Syntax highlighting while typing; ~150 KB more and more moving parts. |
Images (user picked): **compressed on attach and synced** in Phase 2.

## Decisions (user-decided unless marked *[my call]*)

### Data model (store migration v2, additive)
- `items` gains `priority TEXT NOT NULL DEFAULT ''` (`''|'low'|'medium'|'high'`) and `description TEXT NOT NULL DEFAULT ''` (Markdown).
- New table `attachments(id TEXT PK /*ULID*/, item_id TEXT NOT NULL, mime TEXT /*'image/webp'*/, width INT, height INT, bytes INT, sha256 TEXT, created_at, updated_at, updated_by, deleted INT DEFAULT 0)`. File bytes live at `<DataDir>/attachments/<id>.webp`. *[my call: files on disk, not BLOBs, to keep the DB small and fast]*
- `model.Item` gains `Priority string` and `Description string`; a new `model.Attachment` type. Every write stamps `updated_at/updated_by` (Phase 2 sync relies on it).

### Description = Markdown with a helper toolbar (user)
- Toolbar buttons insert/wrap Markdown at the cursor: **Bold, Italic, Heading, Bulleted list, Checklist, Code (inline/block), Link, Image**; plus an **Edit | Preview** toggle. Keyboard: Ctrl+B / Ctrl+I / Ctrl+K (link) / Ctrl+Shift+C (code) / Ctrl+Shift+8 (list) / Ctrl+Shift+9 (checklist) *[my call]*. Each button has a tooltip showing its shortcut and the Markdown it inserts ("hint" for learning).
- Preview renders with `marked`, sanitized by `DOMPurify`; both dynamically imported the first time a panel opens *[my call]*. Checklist items in Preview are real checkboxes; ticking one rewrites the matching `- [ ]`/`- [x]` line in the source and saves.
- Links open in the system browser via the Wails browser-open API; only `http`, `https`, `mailto` allowed; the webview never navigates. Raw HTML in Markdown is stripped.
- **Images inline in the description**: Ctrl+V a screenshot, drag-and-drop a file, or the Image button (file picker). Inserted at the cursor as `![name](att:<id>)` with an "Uploading…" placeholder until stored.
  - Compression happens in the frontend: decode → canvas → longest side ≤ 1600 px → WebP q=0.8; if > 600 KB, retry at q=0.6. Accept PNG/JPEG/WebP/GIF (first frame) and BMP; reject inputs > 20 MB with an inline message *[my call]*.
  - Go binding `AddAttachment(itemID string, data []byte) (Attachment, error)` validates it's WebP, size ≤ 2 MB, computes sha256, writes the file atomically, and inserts the row.
  - Rendering resolves `att:<id>` to a URL served by a Go asset-handler route `/att/<id>` that only accepts a ULID id (no path traversal) and only serves existing, non-deleted attachments.
  - Click an image in Preview → lightbox (Esc closes).
  - **Cleanup** *[my call]*: `attachments` has an `unreferenced_since INTEGER NULL` column. `SetDescription` sets it to now for attachments of that item whose `att:<id>` no longer appears, and clears it for those referenced again. The startup sweep tombstones attachments whose `unreferenced_since` is older than 7 days, or whose item has been deleted for 30 days, and removes their files. A freshly pasted, still-referenced image is never swept, whatever its age. Undoing a task delete within those windows keeps its images.

### Priority (user)
- Values: High / Medium / Low / none (default none).
- **Capture box**: a chip row under the input — `High` `Med` `Low` — plus typed tokens, which stay the single source of truth *[my call]*: clicking a chip or pressing **Alt+1/2/3** inserts or replaces a priority token in the text; **Alt+0** removes it. Tokens (standalone, whitespace-separated, never inside quotes): `!high` `!h` `!!!` → High; `!medium` `!med` `!m` → Medium; `!low` `!l` → Low. A bare `!` or `!!` is ordinary punctuation and never a token, so "call Bob !" stays as typed *[my call]*. `Render` always emits the word forms. The parser strips the token from the text and returns `Priority`; `Render` emits `!high`/`!med`/`!low` so EditText round-trips. The active chip highlights to match the parse. Chips are buttons with `aria-pressed`, reachable by Tab only after the input (Tab still opens the time picker first — order: input → time → chips) *[my call]*.
- **Colors**: High = red, Medium = amber, Low = blue, none = no marker; new tokens `--prio-high`, `--prio-med`, `--prio-low` (contrast-checked in both themes, never color-only: chips/labels carry text, list dot has an `aria-label`).
- **List**: an 8 px dot before the text. Sort *[my call]*: tag sections and `#inbox` sort by priority (High→none), then by their current order; **Today/Upcoming keeps time order** (time matters more there) and shows the dot.
- **Export** (`todo.md`): `- [ ] (!high) text …` marker when set.

### Live reminder indicator in the capture box (user)
- When the debounced preview (80 ms) has a due time, the lead icon becomes a **light-red bell** (shared with the light-red alert-card style) with a 160 ms pop (scale .6→1, opacity; reduced-motion: no scale), and a **badge chip "Reminder · Thu 26 Nov, 17:00"** appears at the start of the preview row. When the time words are removed, both fade out (90 ms) and the icon returns to search/to-do. Announced via the existing `role=status` ("Reminder detected for …" / "Reminder removed").
- Must not collide with the Phase 1.5 mic button in the right action slot.

### Detail panel (user + my calls on layout)
```
┌ list (left) ────────────────────┬─ Detail ─────────────────────── ✕ ┐
│ ☐ fix flaky test    #backend    │ ☐ [fix flaky test.............]  (title)
│▶☐ check deploy 17:00  ● High    │ Priority  (High)(Med)(Low)(None)
│ ☐ read RFC                      │ Due       Thu 26 Nov, 17:00 [edit] Repeat [none ▾]
│                                 │ Tags      #backend #ops [+ tag]
│                                 │ Description   [B][I][H][•][☑][</>][🔗][🖼]  Edit|Preview
│                                 │ ┌───────────────────────────────────┐
│                                 │ │ Steps to reproduce…               │
│                                 │ │ [screenshot]                      │
│                                 │ └───────────────────────────────────┘
│                                 │ created 25 Nov · edited 2 min ago · saved ✓
│                                 │ [✓ Mark done] [Snooze ▾]          [Delete]
└─────────────────────────────────┴───────────────────────────────────┘
```
(The bracketed glyphs in the sketch are Lucide SVG icons in the real UI.)
- **Opening**: a single click on a row selects it **and** opens the panel; **Enter** opens the panel and focuses the title; **F2** or double-click on the text keeps the existing quick inline edit *[my call — click/Enter were previously inline edit]*. ↑/↓ in the list switches the panel's task; `Esc` closes the panel first (then clears search, then closes the window). Ctrl+S forces a save.
- **Size/animation**: the panel width is 50% of the list window, min 420 px; if the window is narrower than 1100 px when the panel opens, the window widens to 1100 (capped to the screen) and shrinks back on close *[my call]*. Slide-in 180 ms transform from the right; reduced-motion: no slide.
- **Autosave**: field edits save after 600 ms idle and on blur/close/switch; a small "Saving… / Saved ✓ / Couldn't save — retry" status sits in the footer; no Save button. Edits made elsewhere (alert Done, another window) refresh the panel unless that field is being edited (same rule as list inline edit).
- **Fields → new Go bindings** (each a core mutation under the existing core lock, stamping updated_*, firing OnChange): `SetText(id, text)` (plain text, no parsing), `SetPriority(id, p)`, `SetDue(id, *time.Time)` (nil → to-do), `SetRRule(id, rrule)` (none / daily / weekdays / weekly on the due's weekday), `SetTags(id, []string)` (empty → `inbox`), `SetDescription(id, md)`, `AddAttachment`, `ItemDetail(id) (Item + []Attachment)`. Validation errors show inline per field.
- **Rescheduling semantics** *[my call]*:
  - `SetDue` clears `SnoozeUntil`. Alert keys include the due time, so a new due time gets fresh alerts, and old "fired" marks no longer apply.
  - Because `updated_at` is bumped, lead alerts already in the past at edit time are skipped (ruling R6 from Phase 1). Only the at-time alert fires if the new due time is very close.
  - For a recurring item, the new due time becomes the series anchor.
  - `SetDue(nil)` turns the item into a to-do and clears `RRule` and `SnoozeUntil`.
  - `SetRRule` without a due time returns "Set a due time first". The Repeat control is disabled until a due time exists. "Weekly" uses the due time's weekday.
  - Snooze is hidden for to-dos, as in the list menu.
- **Footer actions**: Mark done / Reopen, Snooze (10m / 1h / tomorrow — reminders only), Delete (with the existing Undo toast; closes the panel).

## Constraints
- No new windows; panel code and Markdown libs are lazy-loaded; idle RAM must not grow when the panel is closed (measure as in the Phase 1 checklist).
- Token discipline, contrast ≥ 4.5:1 in both themes, keyboard-complete, `prefers-reduced-motion`, Lucide icons only (no emoji) — per the UI design guide.
- Sanitized rendering only; attachment route strictly validated.
- Store migration v2 must upgrade an existing Phase 1 database in place (test with a v1 DB fixture).

## Phase 2 (sync) impact, recorded now
`items` docs carry `priority` and `description`; a new `attachments` collection stores `{_id, item_id, mime, width, height, bytes, sha256, data (BinData), updated_at, updated_by, deleted}` with the same last-writer-wins rule. Settings shows Atlas storage used by attachments. Atlas DB user must be scoped to the `awesome_reminder` database only (the user's cluster hosts other projects).

## Left to the builder
Exact toolbar icon choices and preview typography; lightbox details; the chip visual style within the guide; how the Go asset route is registered in Wails v3.1.0; test fixtures.

## Verification
- Go: migration v1→v2 on a fixture DB; parser priority tokens incl. quoted/standalone rules and Render round-trip; core setters; attachment validation, route id validation, cleanup sweep.
- Frontend: tsc; demo-harness previews of panel, toolbar, preview, lightbox, chips, and the reminder badge in light/dark.
- Manual (temp-copy exe): paste a screenshot → stored ≤ 600 KB WebP and renders; checklist tick in Preview persists; panel open/close leaves idle RAM unchanged.
