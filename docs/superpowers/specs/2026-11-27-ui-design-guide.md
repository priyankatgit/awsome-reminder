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
| `--accent` | `#22C55E` | `#16A34A` | primary action, focus ring, "saved" |
| `--on-accent` | `#0F172A` | `#FFFFFF` | text on accent |
| `--time` | `#F59E0B` | `#B45309` | due-time chip, reminder icon |
| `--danger` | `#EF4444` | `#DC2626` | overdue, delete, **listening** |
| `--ring` | `#22C55E` | `#16A34A` | 2px focus outline, 2px offset |

Tag chips get a stable hue from `hash(tag) % 8` over a fixed 8-hue list (desaturated: slate-blue, teal, violet, rose, amber, cyan, lime, orange). Text and background must keep ≥ 4.5:1 contrast in both themes. `#inbox` is always neutral.

**Type:** IBM Plex Sans (400/500/600) for UI text; JetBrains Mono (400/500) for tags, times, hotkeys, and the capture input. Fonts are bundled locally via `@fontsource/ibm-plex-sans` and `@fontsource/jetbrains-mono`, Latin subset, woff2 only. Sizes: capture input 20px; body 14px; secondary 12px (the minimum); section headers 12px uppercase, letter-spacing .06em, `--fg-muted`.

**Spacing** (4-pt scale): 4, 8, 12, 16, 24, 32. **Radius:** windows 14, cards 10, chips/buttons 8, pills 999. **Shadow** (floating windows only): `0 20px 50px rgba(0,0,0,.35), 0 0 0 1px var(--border)`.

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
- Keys: `/` focus search, `↑/↓` move, `Space` toggle done, `Enter` edit, `s` snooze menu, `Del` delete, `Esc` clear search / close.
- Done uses a 180 ms strike-through + fade, then the row moves to Done. Delete shows a bottom toast "Deleted · Undo" for 5 s (`role="status"`).
- Empty state: a centered `inbox` icon, "Nothing here. Press Ctrl+Shift+Space to capture." with the real hotkey shown as keycaps.

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
- "Missed · was due 14:00" uses `--danger`; "now" uses `--accent`.
- Done is the primary button (accent); Snooze and Open are ghost buttons. Buttons are 32 px tall with a 40 px hit area. Never steal focus.

## Settings
- A single scrolling form, 520 px, with labeled fields (labels above, helper text below in `--fg-muted`, errors inline in `--danger`).
- Hotkey recorder: a pill showing keycaps; click → "Press keys…" → captures the combo; shows the registration error inline.
- Lead-time chips with ✕ and an "+ add" input accepting `30m`, `2h`, `1d`.
- Save button at the bottom-right (accent); unsaved changes enable it.

## Pre-delivery checklist (every UI task)
- [ ] No emoji icons; all icon buttons have `aria-label`
- [ ] Visible focus ring on every interactive element; full keyboard path works
- [ ] Text contrast ≥ 4.5:1 in dark **and** light (check tag chips)
- [ ] `prefers-reduced-motion` disables pulses/slides
- [ ] Only opacity/transform animated; no layout shift when the preview or toast appears
- [ ] Raw hex/px only in `tokens.css`; components use variables
- [ ] Fonts load from the bundle (works offline)
