# Black glossy theme, wider capture box, multi-task typing

Date: 28 Nov 2026. Status: built on branch `ui-black-glossy`.

## What the user asked for
1. A wider quick-capture box, so long tasks are easy to type.
2. Graphics and motion that impress (ui-ux-pro-max):
   - the capture box glossy black, with animation;
   - every other screen black by default;
   - the task list animated, with the high-priority dot blinking.
3. Voice accuracy for an Indian accent. This is a separate session (`voice-accuracy`) and is not covered here.
4. A way to add several tasks from the capture box without it closing after each Enter.

## Rulings

**R1 Width.** The capture window is 880 DIP wide (was 640). At the 20 px mono input that fits about 64 characters before scrolling, up from about 45. On a narrower work area (a 1280 px screen at 150% is 853 DIP) it narrows to leave 24 DIP on each side, never below 480 (`captureWidthFor`).

**R2 Palette "Obsidian".** The dark palette is now near-black, replacing slate. These values in `tokens.css` replace the old dark ones (the light palette is unchanged):

| Token | Before | Now |
|---|---|---|
| `--bg` | `#0F172A` | `#07070A` |
| `--surface` | `#1B2336` | `#111116` |
| `--surface-glass` | `rgba(15,23,42,.72)` | `rgba(9,9,12,.86)` |
| `--muted` | `#272F42` | `#1C1C23` |
| `--border` | `#334155` | `#2A2A33` |
| `--fg-muted` | `#94A3B8` | `#A1A1AA` (7.9:1 on `--bg`, 6.6:1 on `--muted`) |

The neutral grey has no blue cast, so a black surface reads as glossy rather than navy. Reminder, priority, tag and sync hues are unchanged, but their dark backgrounds move to black-based tints so they don't look navy on black. Every text pair keeps ≥ 4.5:1 (figures in `tokens.css`).

**R3 Gloss.** Glossy surfaces get three layers, all tokens:
- a top specular highlight, `--gloss` (a white gradient fading by 45% height);
- a 1 px inner top edge, `--gloss-edge`;
- a deep drop shadow (floating windows only).

These apply to the capture box, alert cards, the details panel, menus, keycaps, primary buttons and hovered or selected list rows. The toast is drawn inverted (`--fg` fill), so it gets no gloss. In light mode the primary button has no highlight (`--gloss-strong: none`), because white text on `--accent-fill` must stay at 5.0:1.

**R4 Signature accent.** An "aurora" gradient, `--aurora` (violet `#8B5CF6` → cyan `#22D3EE` → emerald `#22C55E`), is decorative only and never carries text. It appears on:
- the capture box top light strip;
- the save flash line;
- the list header underline;
- the focus glow of the capture input.

Focus rings stay the solid `--ring`, for contrast.

**R5 Capture box motion.**
- **Open:** the box springs up from 96% scale with a 6 px blur clearing. A gloss sheen (a diagonal light band) sweeps across it once, over `--dur-sheen` (900 ms).
- **While open:** the aurora strip along the top edge drifts slowly, over `--dur-aurora` (6 s). The input row has a soft aurora under-glow that brightens when text is present.
- **Detected time:** the lead bell glows (`--glow-remind`).
- **Save:** the existing fly to the tray, with the flash line in aurora.

**R6 Motion rule change (ambient loops).** The guide said "nothing loops while idle". Two loops are now allowed, because the user asked for them:
- the capture box aurora, which runs only while the box is visible;
- the blinking dot on open High-priority rows.

Both animate only `opacity`/`transform`/`background-position` on small layers, and are static under `prefers-reduced-motion` (the ripple ring rests invisible). Nothing else loops.

**R7 List motion.**
- **Opening the list:** rows cascade in, fading up 8 px with a blur clearing, `--stagger-row` (28 ms) apart, capped at 14 rows. Section headers slide in from the left. This runs once per window open, not on re-renders.
- **High priority:** the dot blinks (opacity 1 → .35 → 1 over `--dur-blink`, 1.6 s). A ring ripples out from it (scale 1 → 2.6, fading) on the same beat. Done rows don't blink.
- **Overdue rows:** a thin red glow bar on the left edge.
- **Hover/selected rows:** these get the gloss highlight.

**R8 Theme choice.** Black is the default on every window, whatever the OS theme. Settings › Appearance offers **Black** (default) or **Match Windows** (black when Windows is dark, the existing light palette when it is light).
- The choice is a per-device UI preference in the webview's `localStorage` (`ar.theme`), not a Go setting.
- All windows share one origin, so a change reaches every open window through the `storage` event, with no new binding.
- It survives restarts, because the webview profile lives in the data folder.
- `<html data-theme="dark|light">` drives the CSS. The old `@media (prefers-color-scheme: light)` blocks became `:root[data-theme="light"]`.
- The native window background (`pageBackground`, setup) is black, so there is no flash on open.

**R9 Multi-task typing.**
- **Shift+Enter** moves the typed line into the review list (the same list "new task" fills when dictating), clears the input and keeps the box open. **Enter** saves the list plus the input in one go, with "save N" in the hint as now.
- **Pasting several lines** puts each non-empty line in the list, except the last, which goes into the input.
- Rows survive a blur (the box keeps its draft, as before). Esc asks before discarding rows (existing behaviour). ↑ edits the last row.
- The hint row adds `Shift+↵ add another`.

  Rejected: "save each and stay open". That gives no review before saving, and no way back except Undo per item. Rejected: a "keep open" pin, which adds a mode to remember.

## Follow-ups (same day, "go with your thoughts")

**R10 Ctrl+Enter: save and open the list.**
- It saves what Enter would save: the review rows plus the input, or the reply in reply mode.
- The usual fly to the tray plays, the box hides, then the list window opens on the last saved task (`OpenList(id)`).
- The hint row shows `Ctrl+↵ save & open list`.

**R11 Undo for a typed batch.**
- When Enter saves more than one task, the box doesn't hide straight away. It flashes, then shows the "Saved N tasks · Undo" note for 5 s (Ctrl+Z works too while the input is empty; once you type the next task it is the text undo again), the same as a spoken "save all".
- Undo deletes the batch and puts the tasks back in the review list.
- The box hides when the note ends, unless you have started typing the next task.
- A single task saves and hides as before. Ctrl+Enter skips the note, because the list has its own Undo.

**R12 The blink settles.**
- The High-priority dot blinks for `--dur-blink-active` (9.6 s, six whole blinks, so it stops at full brightness) after the list window opens or gets focus again.
- After that it rests, with its glow but no blink and no ripple (`.list-app.is-calm`). An open list therefore never pulses all day.
- Coming back to the window (focus) starts a new spell.

## Not changed
Keyboard behaviour, the parser, the alert window's size logic, the font choices, the light palette and the voice code.
