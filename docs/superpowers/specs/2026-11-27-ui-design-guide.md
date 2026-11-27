# Awesome Reminder — UI Design Guide

Binding for every frontend task (capture, list, alert, settings, and the later voice phase). Built from the ui-ux-pro-max recommendation for developer tools (Minimalism/Swiss style, "code dark + run green" palette, JetBrains Mono + IBM Plex Sans). Glassmorphism is used only on the floating surfaces.

## Principles
1. **Keyboard first, mouse friendly.** Every action has a key; every icon button has an accessible name and a visible focus ring.
2. **Quiet until needed.** Neutral slate surfaces with one accent color. Color carries meaning: time, overdue, listening.
3. **Instant.** No web fonts from the network, no animation libraries, no layout shift. Motion takes at most 180 ms and is skipped under `prefers-reduced-motion`.
4. **Native feel.** The floating windows use the OS translucent backdrop (Windows 11 Acrylic/Mica, macOS vibrancy) and fall back to a solid surface on Linux.

## Tokens (`frontend/src/tokens.css`, the only place raw values live)
Dark is the default; light follows `prefers-color-scheme: light`.

| Token | Dark | Light | Use |
|---|---|---|---|
| `--bg` | `#0F172A` | `#F8FAFC` | window background |
| `--surface` | `#1B2336` | `#FFFFFF` | cards, rows |
| `--surface-glass` | `rgba(15,23,42,0.72)` | `rgba(255,255,255,0.78)` | capture box / alert over the OS backdrop |
| `--muted` | `#272F42` | `#EEF2F7` | hover, chips background |
| `--fg` | `#F8FAFC` | `#0F172A` | primary text |
| `--fg-muted` | `#94A3B8` | `#475569` | secondary text, hints |
| `--border` | `#334155` | `#E2E8F0` | dividers (1px) |
| `--accent` | `#22C55E` | `#16A34A` | focus ring, "saved" flash, decorative accent (not text, not button fill in light) |
| `--accent-fill` | `var(--accent)` | `#15803D` | primary button fill (white text 5.0:1; `#16A34A` gives only 3.3:1) |
| `--accent-text` | `#4ADE80` | `#15803D` | accent used as text (the hotkey recorder prompt): 10.3:1 dark, 5.0:1 light |
| `--on-accent` | `#0F172A` | `#FFFFFF` | text on `--accent-fill` |
| `--time` | `#F59E0B` | `#B45309` | reminder icon, decorative time color |
| `--time-text` | `#F59E0B` | `#92400E` | due-time text on rows that may sit on `--muted` |
| `--time-chip-fg` / `--time-chip-bg` | `#F59E0B` / `rgba(245,158,11,.14)` | `#92400E` / `#FEF3C7` | due-time chip (6.6:1 dark, 6.4:1 light) |
| `--danger` | `#EF4444` | `#DC2626` | overdue/delete/**listening** icons and borders |
| `--danger-text` | `#F87171` | `#B91C1C` | danger as text on `--muted` hover rows (4.8:1 dark) |
| `--ring` | `#22C55E` | `#16A34A` | 2px focus outline, 2px offset |
| `--remind` | `#F87171` | `#DC2626` | reminder red: alert card edge bar and bell (graphic, not text); Phase 1.6 capture-box bell |
| `--remind-tint` | `#2A1A24` | `#FEF2F2` | alert card fill (opaque, so contrast is fixed) |
| `--remind-tint-strong` | `#3A1C26` | `#FEE2E2` | missed/overdue alert card fill |
| `--remind-text` | `#FCA5A5` | `#B91C1C` | alert time line: 8.7 / 8.1:1 dark, 5.9 / 5.3:1 light (on tint / strong tint) |
| `--remind-border` | `var(--border)` | `#FCA5A5` | ghost-button borders on the tint (light `--border` vanishes on it) |
| `--remind-glow` | `0 0 12px 2px rgba(248,113,113,.55)` | `0 0 12px 2px rgba(220,38,38,.4)` | one 600 ms (`--dur-remind-glow`) glow of the edge bar when a card appears |
| `--highlight` | `rgba(34,197,94,.16)` | `rgba(22,163,74,.16)` | row highlight after the list opens on an item; fades out |
| `--prio-high` | `#F87171` | `#B91C1C` | High priority dot, chip and label (Phase 1.6) |
| `--prio-med` | `#FBBF24` | `#92400E` | Medium priority |
| `--prio-low` | `#60A5FA` | `#1D4ED8` | Low priority |
| `--panel-width` | `max(50%, 420px)` | same | details panel width (`--panel-share`, `--panel-min`); slide-in `--dur-panel` 180 ms |
| `--shadow-menu` | `0 8px 24px rgba(0,0,0,.28)` + 1px `--border` | `0 8px 24px rgba(15,23,42,.12)` + 1px `--border` | row `⋯` menu (softer than the window shadow) |
| `--sync-ok-fg` / `--sync-ok-bg` | `#4ADE80` / `#12301F` | `#166534` / `#DCFCE7` | "All synced" pill (8.21 dark, 6.49 light). The plan's light `#15803D` gave only 4.57:1 and was darkened to `#166534` |
| `--sync-muted-fg` / `--sync-muted-bg` | `#A8B3C7` / `#272F42` | `#475569` / `#EEF2F7` | Offline pill (6.32 / 6.74) |
| `--sync-danger-fg` / `--sync-danger-bg` | `#FCA5A5` / `#3A1C26` | `#B91C1C` / `#FEE2E2` | Sync error pill (8.05 / 5.30) |
| `--sync-glow` | `0 0 10px 2px rgba(74,222,128,.6)` | `0 0 10px 2px rgba(22,163,74,.45)` | one glow of the pill when the last waiting change reaches Atlas |
| `--sync-pill-height`, `--sync-popover-width` | `24px`, `296px` | same | pill and details popover size |
| `--dur-sync-glow`, `--dur-marker-fade`, `--dur-sync-spin` | `600ms`, `180ms`, `1000ms` | same | glow, marker fade-out, spinner turn |

Rule: text uses the `-text` / `-fg` variants, never the plain `--accent` / `--time` / `--danger`, so contrast holds on `--bg`, `--surface` and `--muted` in both themes. `frontend/src/tokens.css` is authoritative if this table drifts.

Tag chips get a stable hue from `hash(tag) % 8` over a fixed 8-hue list (desaturated: slate-blue, teal, violet, rose, amber, cyan, lime, orange). Text and background must keep ≥ 4.5:1 contrast in both themes (shipped pairs: dark ≥ 7.4:1). `#inbox` is always neutral (`--tag-inbox-*`).

**Type:** IBM Plex Sans (400/500/600) for UI text; JetBrains Mono (400/500) for tags, times, hotkeys, and the capture input. Fonts are bundled locally via `@fontsource/ibm-plex-sans` and `@fontsource/jetbrains-mono`, Latin subset, woff2 only. Sizes: capture input 20px; body 14px; secondary 12px (the minimum); section headers 12px uppercase, letter-spacing .06em, `--fg-muted`.

**Spacing** (4-pt scale): 4, 8, 12, 16, 24, 32. **Radius:** windows 14, cards 10, chips/buttons 8, pills 999. **Shadow** (floating windows only, `--shadow-float`): `0 20px 50px rgba(0,0,0,.35), 0 0 0 1px var(--border)`; menus use `--shadow-menu`.

**Icons:** Lucide SVG (`lucide` package, import individual icons), 18px, stroke 1.75. **No emoji as icons anywhere**; this overrides the ⏰/☐ in the plan text.

**Motion:** enter 140 ms `cubic-bezier(.2,.8,.2,1)` (opacity + `scale(.98→1)`); exit 90 ms ease-in; hover 150 ms. Animate only opacity/transform.

## Capture box (Spotlight)
```
┌────────────────────────────────────────────────────────────┐
│  ⌕  remind me tomorrow 5pm check deploy #backend      [🎤] │  ← mono 20px input; right action slot
├────────────────────────────────────────────────────────────┤
│  (bell) Reminder · Thu 26 Nov, 17:00   #backend             │  ← preview row, role="status"
│         alerts 1h · 10m · at time                           │
└────────────────────────────────────────────────────────────┘
   ↵ save    Tab time    Esc close                ← 12px hint row, mono keycaps
```
- 640 px wide, height grows with preview (no jump: preview row reserves 1 line min). Glass surface + shadow, radius 14.
- Leading icon: `search` muted → swaps to `bell` (`--time`) when a time is detected, `check-square` when it's a to-do.
- Preview pieces are chips: time chip `--time` tinted, tag chips hashed, warnings amber text with `triangle-alert`.
- **Right action slot (48×48 hit area)** is reserved in Phase 1 and holds the mic button in the voice phase. Phase 1 renders nothing there.
- Save feedback: input flashes an `--accent` underline for 120 ms, then the window hides.
- Errors are shown inline under the input in `--danger`, never as alerts.
- **Priority chips (Phase 1.6):** a row of `High` `Med` `Low` chips under the input (`--prio-chip-height` 24 px, an 8 px `--prio-*` dot plus the word, so it never relies on colour alone). Chips are buttons with `aria-pressed` and `aria-keyshortcuts`, and come after the input and the time picker in Tab order. `Alt+1/2/3` set the priority and `Alt+0` clears it. A chip edits the typed token (`!high` / `!med` / `!low`) in the input, which stays the single source of truth; the active chip follows the parse. The hint row adds `Alt+1–3 priority` and `Alt+0 clear`.
- **Live reminder indicator (Phase 1.6):** when the preview has a due time, the lead icon becomes a `--remind` bell with a 160 ms pop (`--dur-pop`, `scale(--pop-scale → 1)` + opacity; no scale under reduced motion), and a `chip-remind` badge "Reminder · Thu 26 Nov, 17:00" (`--remind-text` on `--remind-tint`) leads the preview row. Removing the time words fades both out (90 ms). `role="status"` announces "Reminder detected for …" and "Reminder removed". The mic keeps the right action slot.

### Listening state (voice phase, designed now)
```
┌────────────────────────────────────────────────────────────┐
│  ◉ Listening…  ▁▃▅▇▅▃▁▂▄▆▄▂  0:04                    [■]   │
│  Speak naturally — "remind me tomorrow at 5 to call Sam"   │
└────────────────────────────────────────────────────────────┘
   auto-stops after a pause · Esc cancel · Ctrl+M stop
```
- The mic button (`mic` icon, `aria-label="Start dictation"`, `aria-pressed`) becomes a `square` stop button in `--danger`.
- A pulsing ring around the button: 1.4 s loop, `scale 1→1.35`, `opacity .5→0`, `--danger`. Under reduced motion it's a static ring.
- Live waveform: 24 bars of 3px width with 2px gap, height driven by RMS level (canvas, ≤ 30 fps), color `--danger` at 80%.
- After stop, the label changes to "Transcribing…" with a 3-dot shimmer. The text then types into the input (fast fade-in, not char-by-char), the normal preview appears, and focus returns to the input. **Enter saves** — dictation never auto-saves.
- `role="status"` announces "Listening", "Transcribing", "Ready to save".

## List window (tray)
```
┌ Awesome Reminder ─────────────────────────────── [ ⌕ Search ] [⚙] ┐
│ TODAY / UPCOMING                                                    │
│  ● 09:30  overdue · standup notes                 #team    ⋯        │  ← overdue in --danger
│  ○ 17:00  check deploy                            #backend ⋯        │
│ #INBOX (2)                                                          │
│  ☐ read RFC 9110                                           ⋯        │
│ #BACKEND (3)                                                        │
│  ☐ fix flaky test                                          ⋯        │
│ ▸ DONE (12)                                                         │
└─────────────────────────────────────────────────────────────────────┘
```
- Single column, max content width 760 px, centered; rows 40 px; the full row is the click target; checkbox 20 px inside a 40 px hit area.
- Row hover shows `--muted` background and reveals the `⋯` menu (snooze 10m / 1h / tomorrow, stop repeating, delete). The menu is also reachable by keyboard (`.` or context-menu key).
- Times in mono `--time`; overdue rows prefix "overdue ·" in `--danger` (not color alone).
- Keys: `/` focus search, `↑/↓` move, `Space` toggle done, `Enter` open the details panel (title focused), `F2` inline edit, `s` snooze menu, `Del` delete, `Esc` close the panel / clear search / close. A click on the row opens the panel once the double-click time has passed; a double-click on the text edits inline.
- Priority (Phase 1.6): an 8 px `--prio-*` dot before the text with an `aria-label` ("High priority"). Tag sections sort High → Medium → Low → none, keeping creation order within each; Today/Upcoming stays in time order.
- Done uses a 180 ms strike-through + fade, then the row moves to Done. Delete shows a bottom toast "Deleted · Undo" for 5 s (`role="status"`).
- Empty state: a centered `inbox` icon, "Nothing here. Press Ctrl+Shift+Space to capture." with the real hotkey shown as keycaps.

## Details panel (Phase 1.6)
```
┌ list ──────────────────────────┬─ panel ──────────────────────────── ✕ ┐
│ ☐ fix flaky test    #backend   │ [fix flaky test..................]      │  ← title, 16 px
│▶☐ check deploy 17:00  ● High   │ Priority  (High)(Med)(Low)(None)        │
│ ☐ read RFC                     │ Due       Thu 26 Nov, 17:00   Repeat ▾  │
│                                │ Tags      #backend #ops  [+ tag]        │
│                                │ [B][I][H][•][☑][</>][🔗][🖼]  Edit|Preview │
│                                │ ┌──────────────────────────────────┐    │
│                                │ │ Markdown textarea / preview       │    │
│                                │ └──────────────────────────────────┘    │
│                                │ created 25 Nov · Saved ✓                │
│                                │ [✓ Mark done] [Snooze ▾]      [Delete]  │
└────────────────────────────────┴─────────────────────────────────────────┘
```
(The bracketed glyphs are Lucide icons in the real UI.)
- An in-page drawer in the list window, never a new window. Width `--panel-width` (50% of the window, min 420 px). A window narrower than 1100 px widens to 1100 px (capped to the screen) while it is open and restores on close. It slides in from the right over `--dur-panel` (180 ms, transform only; no slide under reduced motion). The panel module and `marked` / `dompurify` load on first open.
- Field labels are `--panel-label` (10ch) wide, on `--surface` / `--bg` with the existing colour tokens. Errors show inline per field in `--danger-text`. Repeat is disabled, with the note "Set a due time first", until a due time exists.
- No Save button. The footer status reads "Saving…", "✓ Saved" or "Couldn't save · Retry". Edits made elsewhere refresh the panel except for the field being edited.
- Description toolbar: Bold, Italic, Heading, Bulleted list, Checklist, Code, Link, Image, then an Edit | Preview toggle. The toolbar is one tab stop with arrow keys between buttons. Each tooltip shows the shortcut and the Markdown it inserts. Preview typography uses `--md-h1` 18 px and `--md-h2` 16 px, a `--md-quote-bar` (3 px) quote bar, and code on `--muted`. Images are at most `--md-image-max` (320 px) tall and open a lightbox on `--lightbox-backdrop` (Esc closes it, and focus returns to the preview).
- Footer actions: Mark done (primary; once done it becomes a ghost Reopen), Snooze menu (ghost, reminders only), and Delete (ghost in `--danger-text`, right-aligned; it closes the panel and shows the list's Undo toast).

## Pending-count badge (Phase 1.6)
- A red disc (`#DC2626`, the light-theme `--danger`; white text is 4.8:1) with the count in white Go Bold, centred by its ink bounds. It shows `1`–`9`, then `9+`, and nothing at 0. It is drawn in Go (`internal/badge`), not CSS, because it ends up in OS icons.
- **Tray:** composited over the bottom-right quarter of each tray icon, with the glyph cleared under the disc. In the macOS template icon the number is cut out of the disc, because template images keep only alpha. The tooltip reads "Awesome Reminder — N pending" plus any hotkey error.
- **Windows taskbar:** the overlay icon (`ITaskbarList3::SetOverlayIcon`, 16 px, or 32 px at 150% DPI and above) on the one button owned by a native window. Its accessible text is "N pending". **macOS:** the Dock badge label. **Linux:** tray only.
- Settings › "Show pending count" (switch, default on) turns all of them off.

## Alert popup
```
┌──────────────────────────────────────────┐
│ (bell) in 10 min · 17:00            ✕    │
│ check deploy                              │
│ #backend                                  │
│ [ ✓ Done ]  [ Snooze ▾ ]  [ Open ]        │
└──────────────────────────────────────────┘
```
- 380 px, glass surface, bottom-right with 16 px inset. Slides up 8 px + fades in 160 ms. Multiple alerts stack as cards inside the one window (max 3 visible, then "+N more").
- **Light-red reminder cards** (catch the eye; the list window and capture box stay neutral). Every card has an opaque `--remind-tint` fill, a 4 px `--remind` left edge bar (`--remind-bar`), a `--remind` Lucide `bell` at the start of the time line, and the time line ("in 10 min · 17:00", "now · 17:00", "snoozed · was due …") in `--remind-text`, mono 12px medium.
- Missed and overdue cards ("missed · was due 14:00", "overdue 5m · 14:00") read more urgent: `--remind-tint-strong` fill and a 6 px bar (`--remind-bar-strong`). The words carry the state too, never color alone.
- Entrance: besides the 160 ms slide/fade, the edge bar glows once (`--remind-glow`, opacity 0 → 1 → 0 over 600 ms on a `::after` layer). None under `prefers-reduced-motion`. Only opacity/transform animate; the bar and glow are absolutely positioned, so nothing shifts.
- Contrast on the tint (dark / light, base / strong tint): `--fg` 15.8 / 14.6 and 16.3 / 14.6; `--fg-muted` (repeats, "Snooze for", ghost-button hover is `--muted`) 6.4 / 6.0 and 6.9 / 6.2; `--danger-text` (inline error) 6.0 / 5.5 and 5.9 / 5.3. Tag chips keep their own opaque background, so their ratios are unchanged.
- Done is the primary button (`--accent-fill`, green: actions stay distinct from the alert red); Snooze and Open are ghost buttons (border `--remind-border` on the tint). Buttons are 32 px tall with a 40 px hit area. Never steal focus.
- **Shipped deviation:** Snooze does not open a dropdown. The Snooze button toggles an inline row ("Snooze for" + preset buttons) inside the same card, so the small alert window never needs a popup layer of its own and the choices stay keyboard reachable (`aria-expanded` / `aria-controls`).

## Settings
- A single scrolling form, 520 px, with labeled fields (labels above, helper text below in `--fg-muted`, errors inline in `--danger`).
- Hotkey recorder: a pill showing keycaps; click → "Press keys…" → captures the combo; shows the registration error inline.
- Lead-time chips with ✕ and an "+ add" input accepting `30m`, `2h`, `1d`.
- Save button at the bottom-right (accent); unsaved changes enable it.
- "Show pending count" switch (Phase 1.6) after "Start at login"; its help text names the tray icon plus the taskbar (Windows) or the Dock (macOS).

## Sync status (Phase 2)
Only exists once the user has connected an Atlas cluster; with no connection string nothing below is drawn. Wording is in `internal/app/synctext.go` (tray) and `frontend/src/sync-status.ts` (pill); the two share one vocabulary.

- **Pill** (list header, before the settings button, `--sync-pill-height` 24 px, radius pill): a 14 px Lucide icon and text, in the tone's `--sync-*` colors. States: `Syncing…` (`refresh-cw`, turning once per `--dur-sync-spin`, neutral), `All synced · just now` / `All synced · 2 min ago` (`cloud-check`, ok), `Synced · N waiting` (`cloud`, neutral), `Offline` / `Offline · N changes waiting` (`cloud-off`, muted), `Sync error` (`cloud-exclamation-point`, danger; the installed Lucide has no `cloud-alert`). Relative times re-render every 30 s: "just now", "N min ago", "N h ago", then "on 26 Nov". When the last waiting change reaches Atlas the pill glows once (`--sync-glow`, `--dur-sync-glow`); no glow under `prefers-reduced-motion`.
- **Popover** (click the pill; `aria-expanded` / `aria-controls`): state line, "Last synced 14:32 (2 min ago)", "N changes waiting", the error text, warnings (clock off, scope too broad), "Cluster <host>" (never credentials), **Sync now** and **Sync settings**. Opening focuses Sync now; Esc closes and returns focus to the pill; a click or focus outside closes it. Sync now is `aria-disabled` while syncing so it keeps focus. A `role="status"` line announces state changes.
- **Row marker:** a 14 px `cloud-off` icon after the row text in `--fg-muted` with `aria-label` and `title` "Not synced yet", shown on rows whose changes have not reached Atlas. Once synced it fades out over `--dur-marker-fade`; the slot stays reserved while sync is on, so nothing shifts.
- **Settings section** "Sync across machines": a password-type field for the connection string with a show/hide eye, **Connect**; once connected, the same status line as the pill, cluster host, where the string is stored, "Images in Atlas: N MB of 512 MB", warnings, **Sync now** and **Disconnect** (with an inline confirm). It sits outside the Save flow: its buttons act at once.
- **Tray dot:** drawn over the top-right quarter of the tray icon (diameter 40% of the icon, the glyph cleared 1 px around it). Synced is a solid green dot, syncing alternates a green ring and a solid green dot, offline is a grey ring, error is a red dot, off draws nothing. The tooltip appends `Synced · 2 min ago`, `Syncing…`, `Offline · N changes waiting` (`1 change waiting`; just `Offline` at 0) or `Sync error` (the reason shows in the pill and Settings; the tooltip is capped at the 127 characters Windows allows, ending in an ellipsis).
- **Shipped deviation (macOS template icon):** the macOS tray icon is a template image, which the system draws from alpha alone and tints itself, so colour cannot carry the state. There the dot is black and the state is its shape: synced is a filled dot, offline a ring, error a filled dot with a knocked-out bar, and syncing alternates ring and filled. Windows and Linux use the colours above. State is therefore never colour alone on any OS.
- Contrast: popover text on `--surface`, `--fg-muted` 6.11 / 7.58 (dark / light), `--danger-text` 5.66 / 6.47, `--time-text` 7.30 / 7.09; the row marker on `--bg` 6.96 / 7.24.

## Pre-delivery checklist (every UI task)
- [ ] No emoji icons; all icon buttons have `aria-label`
- [ ] Visible focus ring on every interactive element; full keyboard path works
- [ ] Text contrast ≥ 4.5:1 in dark **and** light (check tag chips)
- [ ] `prefers-reduced-motion` disables pulses/slides
- [ ] Only opacity/transform animated; no layout shift when the preview or toast appears
- [ ] Raw hex/px only in `tokens.css`; components use variables
- [ ] Fonts load from the bundle (works offline)
