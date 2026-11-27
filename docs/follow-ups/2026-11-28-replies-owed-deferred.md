# Replies owed: deferred minor findings

Minor findings from the per-task and whole-branch reviews of replies owed (merged as ede4779) that were judged fine to leave for now. Each line is task number: finding. Fixed ones are removed.

- T4: generic Teams/Slack titles give bogus names ("Calendar", "Ops", "general", leading "* ")
- T4: From: "Sharma, Priya" keeps quote chars
- T4: QuoteMarkdown ignores lone \r; empty text gives "> "
- T4: multi-line text whose first line is "Call me - 10:30" still yields OwedTo "Call me" (heuristic limit)
- T8: restore failures only logged; Grabbed=true despite partial restore
- T8: any clipboard change (rdpclip, clipboard manager) counts as the copy
- T8: release wait counts iterations not wall time
- T8: any clipboard change within 1 s of a missed grab triggers the restore (user's own Ctrl+C, password-manager auto-clear) — mitigation: restore only if GetClipboardOwner's process is the grab target
- T8: worst-case Grab return ~650 ms (R5 covers the hold part)
- T4: SourceName strips "extension", so Linux WM_CLASS org.gnome.X → org.gnome
- T9: Linux worst case ~1.2 s (xprop + primary + clipboard serial) — one overall deadline
- T9: xprop under XWayland may name a stale X11 window — skip xprop on Wayland sessions
- T9: macOS denylist matches localizedName (misses localized/CE names) — use bundleIdentifier
- T9: macOS changeCount baseline read after pbSave — a user copy in between gets overwritten
- T9: exec without cmd.WaitDelay can block past timeout
- T9: pbRestore clears then writes; a failed write leaves pasteboard empty — retry once
- T9: main hotkey key not checked on macOS (reviewer: no synthetic key-ups, so no auto-repeat risk)
- T9: darwin dispatch_sync to main queue has no timeout and ignores ctx while restoreMu held — Task 10 must call Grab off-main, never during shutdown
- T9: keycode cache keyed on current layout only; ASCII fallback tries only with Command
- T1: TestMigrateV3ToV4 doesn't assert outbox rows preserved
- T2: conformance doesn't test clearing reply fields on a newer version
- T3: WorkDays only canonicalised on JSON load; Equal order-sensitive → spurious shared re-stamp
- T3: missing-field defaults use runtime.GOOS not LoadSettings goos
- T3: Validate doesn't check capture Hotkey itself; unparseable → clash check skipped
- T3: ApplyRemoteShared relies on unwritten invariant (end_of_day ⇒ sweep present) — comment it
- T5: dropped recurring reply keeps RRule; Delete+Undelete brings it back open with outcome "dropped"
- T5: no tests for DropReply on recurring/deleted, nor OnChange/outbox on SetOwedTo/DropReply
- T6: changing DefaultTime/WorkDays/Loc changes nudge keys → an immediate missed nudge for every open reply (consistent with leads today)
- T6: collapsed earlier nudges never marked covered — a backward clock jump can re-fire one
- T7: Tomorrow pressed after midnight skips a day (use today if work day and before DefaultTime)
- T7: SnoozeReplies drops cards for IDs read back from View, not the set snoozed — have SnoozeRepliesUntil return IDs
- T7: SnoozeRepliesUntil partial failure doesn't fire OnChange
- T7: queued sweep card keeps listing a person after their reply closes (transient)
- T10: capture + reply press at the same moment can desync the reply-open flag (self-heals)
- T10: tooltip truncated at 127 chars by Wails; reply error last (Task 13 inline error covers it)
- T10: press passing ctx check at shutdown can hang InvokeSync up to the 2 s Stop cap; late Add after Wait loses a restore
- T11: fresh reply open discards a normal draft left in the hidden box (no undo) — product decision for final review
- T11: Esc reverts person edit while blur commits (asymmetry, defensible)
- T12: sweep card time line could read "end of day · 17:30"
- T12: with panel open, title can shrink to ~20% (row-reply 40% + tags 40%)
- T12: list.test.ts leaks document keydown listeners per mount; keyboard test order-dependent
- T12: .row-reply max-width 40% raw value (precedent .row-tags)
- T12: no test for "tomorrow" snooze on an undated reply
- T13: focus after failed save lands on first chip; .day-chip focus/reduced-motion rely on global rules
- Final: macOS WebKit click on × may not return focus to the line
- Final: wl-paste --type text/plain is exact; text/plain;charset=utf-8-only sources fall through to xclip (consider --type text)
- Final: KeepText reply press wipes an existing quote (predates the fix)
- Final: an OwedTo containing a single " unbalances Line quoting

Also: Phase 2 sync writes nothing to awesome.log, which made a real-Atlas check slow to diagnose. Add connect/push/pull/error log lines.
