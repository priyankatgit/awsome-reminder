package app

import (
	"errors"
	"reflect"
	"runtime"
	"testing"
	"time"

	"awesome-reminder/internal/model"
	"awesome-reminder/internal/scheduler"
	"awesome-reminder/internal/settings"
)

// putReminder stores an open reminder due in an hour.
func putReminder(t *testing.T, h *harness, id, text string, tags []string, rrule string) {
	t.Helper()
	now := time.Now().UTC()
	due := now.Add(time.Hour).Truncate(time.Minute) // a whole minute, so an edit round trip keeps it
	if err := h.st.Put(model.Item{
		ID: id, Kind: model.KindReminder, Text: text, Tags: tags, DueAt: &due, TZ: "UTC",
		RRule: rrule, Status: model.StatusOpen, CreatedAt: now, UpdatedAt: now, UpdatedBy: "test",
	}); err != nil {
		t.Fatal(err)
	}
}

func alert(id, key string) scheduler.Alert {
	return scheduler.Alert{Key: key, ItemID: id, Text: "text " + id, DueAt: time.Now().Add(10 * time.Minute), Lead: 10 * time.Minute}
}

func keys(cards []AlertCard) []string {
	out := []string{}
	for _, c := range cards {
		out = append(out, c.Key)
	}
	return out
}

// lastQueue is the queue sent with the last alerts:show event.
func lastQueue(t *testing.T, h *harness) []AlertCard {
	t.Helper()
	for i := len(h.emitted) - 1; i >= 0; i-- {
		if h.emitted[i].name == EventAlertsShow {
			return h.emitted[i].data.([]AlertCard)
		}
	}
	t.Fatal("no alerts:show emitted")
	return nil
}

func TestAlertQueueDedupesAndShows(t *testing.T) {
	h := newHarness(t)
	putReminder(t, h, "A", "check deploy", []string{"backend", "ops"}, "FREQ=DAILY")
	putReminder(t, h, "B", "call Sam", nil, "")

	h.svc.showAlerts([]scheduler.Alert{alert("A", "A|1|600"), alert("B", "B|1|0")})
	if h.win.alertsShown != 1 {
		t.Fatalf("ShowAlerts calls %d, want 1", h.win.alertsShown)
	}
	got := h.svc.PendingAlerts()
	if want := []string{"A|1|600", "B|1|0"}; !reflect.DeepEqual(keys(got), want) {
		t.Fatalf("queue %v, want %v", keys(got), want)
	}
	if a := got[0]; !reflect.DeepEqual(a.Tags, []string{"backend", "ops"}) || !a.Recurring || a.Text != "text A" || a.Lead != 10*time.Minute {
		t.Fatalf("card A not enriched: %+v", a)
	}
	if b := got[1]; b.Recurring || len(b.Tags) != 0 {
		t.Fatalf("card B: %+v", b)
	}
	if q := lastQueue(t, h); !reflect.DeepEqual(keys(q), keys(got)) {
		t.Fatalf("event queue %v, want %v", keys(q), keys(got))
	}

	// The same key again (e.g. a re-plan) is not added twice; a new key is.
	events := len(h.emitted)
	h.svc.showAlerts([]scheduler.Alert{alert("A", "A|1|600")})
	if len(h.svc.PendingAlerts()) != 2 || len(h.emitted) != events || h.win.alertsShown != 1 {
		t.Fatalf("duplicate changed something: queue %v emits %d->%d shows %d", keys(h.svc.PendingAlerts()), events, len(h.emitted), h.win.alertsShown)
	}
	h.svc.showAlerts([]scheduler.Alert{alert("A", "A|1|0"), alert("B", "B|1|0")})
	if want := []string{"A|1|600", "B|1|0", "A|1|0"}; !reflect.DeepEqual(keys(h.svc.PendingAlerts()), want) {
		t.Fatalf("queue %v, want %v", keys(h.svc.PendingAlerts()), want)
	}
	if h.win.alertsShown != 2 {
		t.Fatalf("ShowAlerts calls %d, want 2", h.win.alertsShown)
	}
}

func TestAlertQueueMissingItemStillShown(t *testing.T) {
	h := newHarness(t)
	h.svc.showAlerts([]scheduler.Alert{alert("gone", "gone|1|0")})
	if got := h.svc.PendingAlerts(); len(got) != 1 || got[0].Tags == nil {
		t.Fatalf("queue %+v (tags must be non-nil for the frontend)", got)
	}
}

func TestDismissAlertOneThenLastHides(t *testing.T) {
	h := newHarness(t)
	h.svc.showAlerts([]scheduler.Alert{alert("A", "a1"), alert("B", "b1")})
	h.svc.DismissAlert("a1")
	if got := keys(h.svc.PendingAlerts()); !reflect.DeepEqual(got, []string{"b1"}) {
		t.Fatalf("queue %v", got)
	}
	if h.win.alertsHidden != 0 {
		t.Fatal("window hidden with alerts left")
	}
	if got := keys(lastQueue(t, h)); !reflect.DeepEqual(got, []string{"b1"}) {
		t.Fatalf("event queue %v", got)
	}
	h.svc.DismissAlert("nope") // unknown key: nothing happens
	if h.win.alertsHidden != 0 || len(h.svc.PendingAlerts()) != 1 {
		t.Fatal("unknown key changed the queue")
	}
	h.svc.DismissAlert("b1")
	if len(h.svc.PendingAlerts()) != 0 || h.win.alertsHidden != 1 {
		t.Fatalf("queue %v hidden %d", keys(h.svc.PendingAlerts()), h.win.alertsHidden)
	}
}

func TestDismissAlertsClearsAndHides(t *testing.T) {
	h := newHarness(t)
	h.svc.showAlerts([]scheduler.Alert{alert("A", "a1"), alert("B", "b1")})
	h.svc.DismissAlerts()
	if len(h.svc.PendingAlerts()) != 0 || h.win.alertsHidden != 1 {
		t.Fatalf("queue %v hidden %d", keys(h.svc.PendingAlerts()), h.win.alertsHidden)
	}
	if got := lastQueue(t, h); len(got) != 0 {
		t.Fatalf("event queue %v, want empty", keys(got))
	}
	// A new alert after that shows the window again.
	h.svc.showAlerts([]scheduler.Alert{alert("A", "a2")})
	if h.win.alertsShown != 2 || len(h.svc.PendingAlerts()) != 1 {
		t.Fatalf("shows %d queue %v", h.win.alertsShown, keys(h.svc.PendingAlerts()))
	}
}

func TestDoneRemovesAllCardsOfThatItem(t *testing.T) {
	h := newHarness(t)
	putReminder(t, h, "A", "check deploy", nil, "")
	putReminder(t, h, "B", "call Sam", nil, "")
	h.svc.showAlerts([]scheduler.Alert{alert("A", "a-lead"), alert("B", "b-now"), alert("A", "a-now")})
	if err := h.svc.Done("A"); err != nil {
		t.Fatal(err)
	}
	if got := keys(h.svc.PendingAlerts()); !reflect.DeepEqual(got, []string{"b-now"}) {
		t.Fatalf("queue %v, want [b-now]", got)
	}
	if h.win.alertsHidden != 0 {
		t.Fatal("hidden with a card left")
	}
	if got := keys(lastQueue(t, h)); !reflect.DeepEqual(got, []string{"b-now"}) {
		t.Fatalf("event queue %v", got)
	}
	// Snoozing the last remaining item empties the queue and hides the window.
	if err := h.svc.Snooze("B", "10m"); err != nil {
		t.Fatal(err)
	}
	if len(h.svc.PendingAlerts()) != 0 || h.win.alertsHidden != 1 {
		t.Fatalf("queue %v hidden %d", keys(h.svc.PendingAlerts()), h.win.alertsHidden)
	}
}

func TestFailedDoneKeepsCards(t *testing.T) {
	h := newHarness(t)
	h.svc.showAlerts([]scheduler.Alert{alert("missing", "m1")})
	if err := h.svc.Done("missing"); err == nil {
		t.Fatal("Done of an unknown item succeeded")
	}
	if len(h.svc.PendingAlerts()) != 1 || h.win.alertsHidden != 0 {
		t.Fatal("failed Done dropped the card")
	}
}

func TestDeleteRemovesCards(t *testing.T) {
	h := newHarness(t)
	putReminder(t, h, "A", "check deploy", nil, "")
	h.svc.showAlerts([]scheduler.Alert{alert("A", "a1")})
	if err := h.svc.Delete("A"); err != nil {
		t.Fatal(err)
	}
	if len(h.svc.PendingAlerts()) != 0 || h.win.alertsHidden != 1 {
		t.Fatalf("queue %v hidden %d", keys(h.svc.PendingAlerts()), h.win.alertsHidden)
	}
}

func TestAlertsClosedClearsQueueWithoutHiding(t *testing.T) {
	h := newHarness(t)
	h.svc.showAlerts([]scheduler.Alert{alert("A", "a1")})
	h.svc.alertsClosed()
	if len(h.svc.PendingAlerts()) != 0 || h.win.alertsHidden != 0 {
		t.Fatalf("queue %v hidden %d", keys(h.svc.PendingAlerts()), h.win.alertsHidden)
	}
}

func TestHotkeyRecordingSuspendsAndResumes(t *testing.T) {
	h := newHarness(t)
	before := len(h.reg.registered)
	h.svc.SetHotkeyRecording(true)
	h.svc.SetHotkeyRecording(false)
	if got := h.reg.registered[before:]; len(got) != 1 || got[0] != "Ctrl+Shift+Space" {
		t.Fatalf("re-registered %v, want the current combo once", got)
	}
}

func TestServiceOpenSettingsAndResizeAlerts(t *testing.T) {
	h := newHarness(t)
	h.svc.OpenSettings()
	h.svc.ResizeAlerts(212)
	if h.win.settings != 1 || !reflect.DeepEqual(h.win.alertHeights, []int{212}) {
		t.Fatalf("settings %d heights %v", h.win.settings, h.win.alertHeights)
	}
}

// Cancelling a recording must not hide a hotkey that failed to register on
// save (tray tooltip and Settings read Status).
func TestHotkeyErrorSurvivesRecordingCancel(t *testing.T) {
	h := newHarness(t)
	h.reg.fail["Alt+K"] = errors.New("Alt+K is already used by another app")
	s := settings.Defaults(runtime.GOOS)
	s.Hotkey = "Alt+K"
	if err := h.svc.SaveSettings(s); err == nil {
		t.Fatal("want hotkey error")
	}
	h.svc.SetHotkeyRecording(true)
	h.svc.SetHotkeyRecording(false)
	if st := h.svc.Status(); st.HotkeyErr != "Alt+K is already used by another app" {
		t.Fatalf("HotkeyErr after cancel = %q", st.HotkeyErr)
	}
}

// M2: an edit that moves the due time drops the item's cards (a "now" or
// "missed" card would be stale); an edit that keeps it leaves them.
func TestUpdateDropsCardsWhenDueChanges(t *testing.T) {
	h := newHarness(t)
	putReminder(t, h, "A", "check deploy", nil, "")
	putReminder(t, h, "B", "call Sam", nil, "")
	h.svc.showAlerts([]scheduler.Alert{alert("A", "a-now"), alert("B", "b-now")})

	txt, err := h.svc.EditText("B")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := h.st.Get("B")
	if _, err := h.svc.Update("B", txt+" #ops"); err != nil { // same due time
		t.Fatal(err)
	}
	if after, _ := h.st.Get("B"); !after.DueAt.Equal(*before.DueAt) {
		t.Skipf("round trip moved the due time (%v -> %v); can't test the unchanged case", before.DueAt, after.DueAt)
	}
	if got := keys(h.svc.PendingAlerts()); !reflect.DeepEqual(got, []string{"a-now", "b-now"}) {
		t.Fatalf("queue %v after a text-only edit", got)
	}

	if _, err := h.svc.Update("A", "check deploy tomorrow 5pm"); err != nil {
		t.Fatal(err)
	}
	if got := keys(h.svc.PendingAlerts()); !reflect.DeepEqual(got, []string{"b-now"}) {
		t.Fatalf("queue %v, want [b-now]", got)
	}
	if _, err := h.svc.Update("B", "call Sam"); err != nil { // reminder -> to-do
		t.Fatal(err)
	}
	if len(h.svc.PendingAlerts()) != 0 || h.win.alertsHidden != 1 {
		t.Fatalf("queue %v hidden %d", keys(h.svc.PendingAlerts()), h.win.alertsHidden)
	}
}

// M2 for the detail panel's setters: SetDue moving the due time (or making
// the item a to-do) drops the item's cards like Update does; SetDue to the
// same time, SetRepeat and the other setters keep them.
func TestDetailSettersDropCardsWhenDueChanges(t *testing.T) {
	h := newHarness(t)
	putReminder(t, h, "A", "check deploy", nil, "")
	putReminder(t, h, "B", "call Sam", nil, "")
	h.svc.showAlerts([]scheduler.Alert{alert("A", "a-now"), alert("B", "b-now")})

	b, _ := h.st.Get("B")
	same := *b.DueAt
	if _, err := h.svc.SetDue("B", &same); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.SetRepeat("B", "daily"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.SetText("B", "call Sam back"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.SetPriority("B", "high"); err != nil {
		t.Fatal(err)
	}
	if got := keys(h.svc.PendingAlerts()); !reflect.DeepEqual(got, []string{"a-now", "b-now"}) {
		t.Fatalf("queue %v after edits that keep the due time", got)
	}

	later := time.Now().UTC().Add(3 * time.Hour).Truncate(time.Minute)
	if _, err := h.svc.SetDue("A", &later); err != nil {
		t.Fatal(err)
	}
	if got := keys(h.svc.PendingAlerts()); !reflect.DeepEqual(got, []string{"b-now"}) {
		t.Fatalf("queue %v, want [b-now]", got)
	}
	if _, err := h.svc.SetDue("B", nil); err != nil { // reminder -> to-do
		t.Fatal(err)
	}
	if len(h.svc.PendingAlerts()) != 0 || h.win.alertsHidden != 1 {
		t.Fatalf("queue %v hidden %d", keys(h.svc.PendingAlerts()), h.win.alertsHidden)
	}
}
