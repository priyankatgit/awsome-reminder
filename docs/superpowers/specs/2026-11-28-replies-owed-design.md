# Replies owed: one-key capture of messages people are waiting on — Design

Builds on `2026-11-27-awesome-reminder-design.md` (Phase 1 local core), `2026-11-28-task-details-priority-design.md` (Phase 1.6) and `2026-11-27-ui-design-guide.md`. Lands **after Phase 2 (Atlas sync) is merged**, because it adds item fields that sync must carry (see Constraints).

## Intent / why
The user is a developer juggling several things at once, and other people depend on them. Someone asks something in team chat, or an email needs a reply. The user means to answer, gets pulled into other work, and the reply slips. Their own tasks are already covered by the app. What isn't covered is **what they owe other people**, because a message that needs a reply never becomes a task.

Success: nobody waits on the user without the user knowing. When they come up from deep work, one glance shows who is waiting, and the app keeps nudging until each reply is sent or consciously dropped.

## Goals
1. **One key to log a reply owed.** While reading the message in Outlook, Teams, Slack, a browser or anything else, the user selects it (or not) and presses a dedicated hotkey (default `Ctrl+Alt+R`, `Ctrl+Option+R` on macOS). The capture box opens in "reply owed" mode, pre-filled with who it is for and a quote of the message. Enter saves.
2. **"Waiting on you" in the list:** replies owed get their own section at the top of the list, grouped by person, oldest first.
3. **Nudges that get firmer:** a reply owed keeps alerting until it is marked replied or dismissed.
4. **End-of-day sweep:** one alert before the end of the working day listing who is still waiting.

## Non-goals
- Connecting to mail or chat accounts, or reading inboxes (that is option B/C from the brainstorm, possibly later).
- Opening the original message from the task: there is no reliable deep link across apps.
- Sending the reply from this app.
- Tracking what *others* owe the user ("waiting on Bob"). That is a possible follow-up, not this design.
- Voice ("reply to Priya …") — Phase 3 hands-free voice may add it later.

## Anti-goals (failure even if it works)
- The normal capture hotkey gets slower or behaves differently. It stays exactly as it is.
- The user's clipboard is lost or changed. Anything grabbed must be put back, and when that can't be guaranteed, the app must not touch the clipboard.
- Text the user did not select ends up in a task or leaves their computer. Everything stays local (Phase 2 sync to the user's own Atlas is the only exception, as for every item).
- A wrong guess is saved silently: the person and the text are always shown, editable, before Enter.
- Nudges that feel like spam, so the user turns them off.

## Approaches considered (user picked 3)
How the hotkey gets hold of the message:

| | Option | Trade-off |
|---|---|---|
| 1 | Copy (Ctrl+C) first, then press the hotkey; the app reads the clipboard | Simplest and most reliable, never injects keys; one extra keystroke every time. |
| 2 | The normal capture hotkey grabs the selection | One key, but every capture gets slower, keys are injected into other apps on every capture, and macOS needs accessibility permission. |
| **3 ✅** | A **separate "reply owed" hotkey** that grabs the selection (and puts the clipboard back), falling back to what was just copied | One key, the normal hotkey is untouched, and the reply key always means "someone is waiting on me". Costs per-OS selection-grab code. |

Earlier, the user also chose **A over B/C**: one keystroke and offline-only, rather than connecting to mail/chat accounts.

## Decisions (user-decided unless marked *[my call]*)

### The hotkey and the grab
- A second global hotkey, configurable in Settings next to the capture hotkey, default `Ctrl+Alt+R` / `Ctrl+Option+R` *[my call]*. Not `Ctrl+Shift+R`: that is Reply All in Outlook and hard reload in every browser, and a global hotkey would silently take those over, because the apps don't register them globally, so no clash error would ever show. A clash with another app is shown the same way as for the capture hotkey (inline error and tray tooltip). It must not be the same combo as the capture hotkey.
- On press, before the capture box takes focus *[my call on all of these]*:
  1. Note the **foreground window**: process name and title (e.g. `OUTLOOK.EXE`, "Chat | Priya Sharma | Microsoft Teams").
  2. **Grab the selection.**
     - Windows: wait until the hotkey's modifiers are released (or release them with synthetic key-ups), so the injected copy is a plain `Ctrl+C`. Holding Shift would make it `Ctrl+Shift+C`, a different command in browsers and Teams. Record the clipboard sequence number, send `Ctrl+C` with `SendInput`, then wait up to ~150 ms for the sequence number to change. If it changed, read the text and **restore the previous clipboard**.
     - macOS: try `AXSelectedText` on the focused element first; it needs Accessibility permission and injects nothing. If that fails, inject `Cmd+C` with a CGEvent the same way.
     - Linux X11: read the PRIMARY selection, which holds the selected text without any copy, so nothing is injected and the clipboard is untouched. On Wayland, where global hotkeys are already unavailable, the user binds `awesome-reminder --reply` in their desktop settings, and the app reads PRIMARY through the portal or `wl-paste --primary` if present, else uses the clipboard.
  3. **Clipboard safety** *[my call]*. Before injecting, save every clipboard format that can be read into memory at that moment (text, HTML, RTF and app-private memory formats); restore them all afterwards. Don't inject at all when the clipboard holds something that can't be saved and put back faithfully: files, bitmaps or metafiles, or delayed-rendered formats. Then fall back as in step 4. Restoring changes clipboard ownership, so something copied from Office loses its Paste Special options. That is accepted.
  4. **Fallback** *[my call]*. If nothing was grabbed (no selection, the app refused, or injection was skipped), offer the current clipboard text as the quote when it is plain text under ~2,000 characters. It is shown and editable, and is easy to clear. There is no "copied in the last 30 s" check: that would need a clipboard watcher running all the time, which this design forbids. If the clipboard is empty or not text, the box opens with just the person guess.
- The grab must add no more than ~200 ms before the box is visible. The capture hotkey's latency target (< 150 ms) does not apply to this key, but it should feel immediate.
- A command-line flag `--reply` asks the running app to do the same (like `--capture`), for Wayland and scripting.

### What gets pre-filled
- **Person:** a best-effort guess *[my call]* from, in order:
  - a `From:` line or a "Name  10:42 AM" chat header in the grabbed text;
  - per-app window-title patterns (Teams "Chat | <name> | Microsoft Teams", Slack "<name> (DM) - <workspace> - Slack", Outlook message windows);
  - else empty, with the cursor on the person field.
- **Source:** the app it came from (Outlook, Teams, Slack, Chrome, …), shown and stored.
- **Quote:** the grabbed text, trimmed to its first ~200 characters for the line and kept in full in the description (Markdown blockquote), so the details panel shows the whole message.
- **Line:** `Reply to <person>: "<first words of the quote>"`. The user can edit everything before Enter. The normal parser still runs on the line, so `!high`, `#tags` and times the user adds work (`… by 5pm` gives a due time); the quote is in double quotes, which the parser keeps literal, so the other person's times and tags are not read as settings (a `"` inside it becomes `”`).
- **Quote row:** the quote that will be saved shows on its own row under the input with a **Remove quote** button (`Alt+Q`); only a quote still shown is saved. A clipboard fallback quote also goes once the line no longer holds its start. The quote is capped at 64 KB.

### The capture box in reply mode
```
┌──────────────────────────────────────────────────────────┐
│ ↩  Reply to Priya: "can you check the deploy before 5?"  │
├──────────────────────────────────────────────────────────┤
│  ▍ can you check the deploy before 5?                 ×  │
├──────────────────────────────────────────────────────────┤
│  (reply) for Priya · from Teams · nudge in 2h, then 9:00 │
└──────────────────────────────────────────────────────────┘
   ↵ save    Alt+P person    Alt+Q remove quote    Tab time    Esc cancel
```
- The same window as the capture box, in a mode: the lead icon is the Lucide `reply` icon, and the preview row shows a "Reply owed" chip, the person, the source and the nudge schedule.
- `Alt+P`, or a click on the person chip, edits the person (a small inline input) *[my call]*. `Tab` keeps opening the time picker, as in normal mode.
- The voice mic still works in this mode.

### Data
- A reply owed is an item with an explicit marker *[my call]*: **`reply`** (true for anything saved in reply mode), **`owedTo`** (the person, free text, may be empty) and **`source`** (the app name). The marker, not a non-empty person, makes it a reply owed, so a save with no person guessed is still nudged and listed (grouped under "Someone"), and clearing the person later doesn't quietly turn it into a to-do. Naming and whether this is a field or a third `Kind` are left to the builder.
- Closing it has two outcomes *[my call]*: **Replied** (done) and **No reply needed** (also done, marked as dropped). Done shows "no reply needed" next to dropped ones, so a dropped reply reads differently from a lost one. Delete still removes it entirely. It gets tags like any item; no tag gives `inbox`.
- It counts in the pending badge (an open item, as now).
- `todo.md` export lists replies owed in their own "Waiting on you" section, `- [ ] Reply to Priya: … (from Teams)`.

### List: "Waiting on you"
- A new section **at the top** of the list, above Today/Upcoming *[my call: it is the user's main headache]*, titled "Waiting on you (N)". Rows are grouped under person subheadings, oldest first by creation time, with the source and an age ("2 h", "yesterday", "3 days") in mono `--fg-muted`. An age over 24 h reads in `--time-text`.
- Replies owed do not also appear in their tag section.
- Row actions: Space = replied; the row menu adds "No reply needed", plus snooze and Delete. The details panel shows person and source as editable fields above the description.

### Nudges
- Schedule *[my call]*:
  1. 2 hours after saving;
  2. then the next working morning at the default time (`09:00`);
  3. then every working day at the default time, until replied or dismissed.
  If the user gave a due time ("by 5pm"), the normal lead-time alerts apply instead, then the daily nudge if it passes unanswered.
- Nudges use the existing alert cards, with the time line "waiting 2 h · Priya", and the buttons **Replied**, **Snooze** and **Open** ("No reply needed" lives in the row menu and the details panel). One card per item, as today *[my call]*: several replies owed to the same person show as separate, adjacent cards, so "Replied" never completes a reply the user hasn't sent.
- Working days are Monday to Friday by default; a Settings field holds the working days and the end-of-day time.

### End-of-day sweep
- At the end-of-day time (default `17:30`, Settings) on a working day, if any replies are owed, one alert card: "3 people are still waiting on you — Priya, Sam, Ops channel", with **Open list** and **Tomorrow** (snooze all to the next morning nudge). No card when nothing is owed. It can be turned off.
- If the computer was asleep or off at that time, it shows once on wake, the same day only.

### Settings additions
Reply hotkey; working days; end-of-day time; "End-of-day sweep" on/off. Defaults: `Ctrl+Alt+R` / `Ctrl+Option+R`, Mon–Fri, 17:30, on.

## Constraints
- **Phase 2 sync:** the new fields must sync as item fields with the same last-writer-wins rule; build after Phase 2 merges, adding them to the Atlas document mapping. The store migration is additive and takes the next free schema version (Phase 2 already uses v3, so v4).
- Everything local; nothing new leaves the machine. The grabbed text is stored only as the item's text and description.
- **Clipboard history (user-decided):** the injected copy is handled by the source app, so the grabbed text lands in OS clipboard history (Windows Win+V and cloud clipboard, macOS Universal Clipboard, clipboard managers), exactly as if the user had pressed Ctrl+C themselves. The user accepted this. The app's own restore write should be marked as excluded from history and cloud sync where the OS allows (on Windows, the `ExcludeClipboardContentFromMonitorProcessing` / `CanIncludeInClipboardHistory` / `CanUploadToCloudClipboard` formats), so restoring doesn't add a second entry.
- No new WebView windows; idle CPU stays ~0 (no polling of the foreground window or clipboard: work happens only on the key press).
- UI rules from the design guide: tokens only, contrast ≥ 4.5:1 in both themes, Lucide icons, keyboard-complete, `prefers-reduced-motion`.
- macOS: if Accessibility permission is missing, explain once inline how to grant it, and use the clipboard fallback meanwhile.

## Left to the builder
How a second hotkey is registered alongside the first (`internal/hotkey` supports one combo per `Manager`); the exact per-app title patterns; the clipboard restore implementation per OS; whether `owedTo` is a field or a kind; the visual details of the person field and the section, within the design guide; test fixtures for title and header parsing.

## Verification
- Go: person/source guessing from titles and text (table tests); the nudge schedule and working days (scheduler tests with a fake clock); the end-of-day sweep, including sleep through it; the store migration; the export section; Phase 2 sync of the new fields.
- Frontend: tsc and vitest for reply mode, the person field and the section grouping; demo-harness previews in light and dark.
- Manual (temp copy, Windows first): select text in Outlook, Teams, Slack and Chrome, press the key; the clipboard holds exactly what it held before (text and an image both); with an image on the clipboard, no copy is injected; nudges and the sweep fire on time.
