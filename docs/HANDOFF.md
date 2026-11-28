# Handoff: Awesome Reminder (end of 28 Nov 2026)

Start here in a new session. Repo: `github.com/priyankatgit/awsome-reminder`. Main checkout: `D:\work\awesome-reminder` on `master`.

## Where things stand

`master` is at **`3297395`**, pushed. **Every roadmap item is built, reviewed and merged.** Go and frontend tests and `wails3 build` all pass on `master`.

| Feature | Merge commit | Spec / plan |
|---|---|---|
| Phase 1: hotkey capture box, plain-English parser, tray list, alerts, Settings, `todo.md` export, autostart | (early) | `docs/superpowers/specs/2026-11-27-awesome-reminder-design.md`, `plans/2026-11-27-phase1-local-core.md` |
| Phase 1.5: local voice dictation (whisper.cpp) | (early) | `plans/2026-11-27-phase1.5-voice.md` |
| Phase 1.6: details panel, Markdown and images, priority, reminder bell, pending badge | `931cbd2` | `specs/2026-11-28-task-details-priority-design.md` |
| Motion polish: springs, FLIP reflow, row grows into the panel, fly to tray, celebration | `a50d7ad` | design guide, Motion section |
| Phase 2: optional MongoDB Atlas sync (your own cluster), logging, hardening | `6de9cbc`, `43c7493`, `b953ea8` | `specs/…`, `plans/2026-11-28-phase2-atlas-sync.md`, `docs/atlas-setup.md` |
| Replies owed: Ctrl+Alt+R grabs a selected message; "Waiting on you", nudges, end-of-day sweep | `ede4779`, `43c7493` | `specs/2026-11-28-replies-owed-design.md`, `plans/2026-11-28-replies-owed.md` |
| Phase 3: "Hey Awesome" wake word (sherpa-onnx), multi-task dictation, review list | `800ecf4` | `specs/2026-11-28-hands-free-voice-design.md`, `plans/2026-11-28-phase3-hands-free-voice.md` |
| Data folder: first-run choice, move/adopt, unavailable screen, Settings section | `4bf6cfb`, `1221d6c` | `specs/2026-11-28-data-folder-design.md`, `plans/2026-11-28-data-folder.md` |
| Phase 4: ntfy phone push, scheduled ahead on the server, Done/Snooze from the phone | `3297395` | `specs/2026-11-28-phone-push-ntfy-design.md`, `plans/2026-11-28-phase4-phone-push.md` |
| Voice accuracy: speech model choice (Base / Small, recommended / Large v3 Turbo), beam search, vocabulary prompt, "Words I use" | see `git log --grep "voice accuracy"` | `specs/2026-11-28-voice-accuracy-design.md`, `plans/2026-11-28-voice-accuracy.md` |

The UI rules are in `docs/superpowers/specs/2026-11-27-ui-design-guide.md`; follow them for any UI work.

## The user's install

- **Fresh build:** `D:\work\awesome-reminder\bin\awesome-reminder.exe` (built from `3297395`).
- **The user runs it from** `D:\program\awesome-reminder\awesome-reminder.exe`. To update, quit the app from the tray, copy the new executable over the old one, and run it.
- **Data:** in the data folder chosen on first run. The default is `%APPDATA%\awesome-reminder\` (holding `awesome.db`, `awesome.log` and attachments). Voice files are in `%LOCALAPPDATA%\awesome-reminder\voice\`, unless they were moved with the data folder.
- **Keychain:** the Atlas connection string and the ntfy token live in the Windows keychain (service `dev.awesome-reminder.app`), per OS user.

## Still to verify (needs the user)

All of these are listed in `docs/manual-test-checklist.md` with "needs human" or a blank result:
- **Replies owed, R1–R6:** select text in Outlook, Teams, Slack or Chrome, then Ctrl+Alt+R; check the clipboard is restored; check Outlook's own Ctrl+Shift+R still works.
- **Phase 3:** a real microphone, saying "Hey Awesome", multi-task dictation, false wake-ups.
- **Phase 4, P1–P10:** a real phone with the ntfy app; pushes while the computer is closed; Done and Snooze from the phone.
- **Phase 2:** sync between two machines (a single machine against real Atlas is already verified).
- **Data folder:** the first run on the user's real profile (adopting or moving the existing `%APPDATA%` data).
- **Voice accuracy, VA1–VA7:** the user's own voice, comparing Base and Small, and switching models in Settings.
- **Earlier visual checks:** pinning the taskbar button, the row-to-panel morph at the default 900 px window, how the alert window shrinks.
- **Not runnable here:** the macOS and Linux builds (the macOS grab and Dock code has never been compiled, since there's no Mac), and `go test -race` (it needs cgo/gcc, which this machine doesn't have).
- **Clean-up:** two "Atlas sync test" items may still be in the user's Atlas `items` collection.

## Known minor follow-ups

`docs/follow-ups/2026-11-28-replies-owed-deferred.md` lists the minor findings that were judged fine to leave. The design docs and plans also record per-phase rulings.

## How the user wants work done

These are also saved as memory files:
- **Decide, don't wait for approval.** Make design and plan calls yourself, record them as rulings, and build. Stop only for destructive actions on real data, or for things only the user can do (credentials, physical checks).
- **No PRs.** Merge into `master` and push without asking, then run the full suite again after every merge: `go build ./... && go vet ./... && go test ./...`, then `npx tsc --noEmit -p .` and `npx vitest run` in `frontend/`, then `wails3 build`.
- **Run independent work in parallel**, in separate sessions and worktrees. Merge `origin/master` in before landing.
- **Method:** brainstorming, then a design doc, then writing-plans, then **subagent-driven development** (per-task implementer plus reviewer, then a whole-branch review), then merge.

## Gotchas

- **Never run `bin/awesome-reminder.exe` from the repo path.** It registers autostart from that path. Use a copy under `%TEMP%` with `APPDATA` and `LOCALAPPDATA` pointed at a temp folder.
- **Temp copies still sync to the real Atlas.** The keychain is per OS user, so a temp copy finds the real connection string and pushes its test items and settings. For checks that create data, use a Go test harness (store + core) instead, or expect the copy to sync and clean up afterwards.
- **Single-instance lock:** only one copy of the app can run. A second launch is handed to the running one. Stop test copies promptly, and check `Get-Process awesome-reminder, ar` before testing.
- **Phase 4 test topics:** the same keychain caveat applies to the ntfy token and topic.
- **Line endings:** working copies are CRLF (`core.autocrlf=true`), so `gofmt -l` flags files. Check the committed content instead (`git show :file | gofmt -l`).
- **Leftover worktrees:** about 11 under `.claude/worktrees/` are from finished work (all merged), and can be removed with `git worktree remove` once you've confirmed they have no uncommitted changes.

- **GPU speech engine:** the laptop has an NVIDIA T600; a CUDA build of whisper.cpp would make Large v3 Turbo usable (it takes about 20 s per phrase on the CPU). See ruling 12 of the voice accuracy spec.

## Next ideas (not planned)

Adding tasks from the phone, calendar integration, meeting notes. Brainstorm the next one with the user first.
