// Dev-only sample data for previewing the views in a plain browser
// (`npx vite --config frontend/vite.config.ts frontend`, then open
// http://127.0.0.1:9245/?demo#list, or ?demo=empty, or ?demo&focus=4;
// ?demo#alert, ?demo=one#alert; ?demo#settings, where saving the hotkey
// Ctrl+Alt+K fails as "already used"). Loaded only when import.meta.env.DEV
// and ?demo are both set, so production bundles never include it.
import { api, emitLocal, events, type AlertCard, type Item, type Settings, type View } from "./api";
import { effectiveDue } from "./item-row";

function item(id: string, text: string, tags: string[], dueInMin: number | null, extra: Partial<Item> = {}): Item {
  const now = Date.now();
  return {
    id,
    kind: dueInMin === null ? "todo" : "reminder",
    text,
    tags,
    dueAt: dueInMin === null ? null : new Date(now + dueInMin * 60_000).toISOString(),
    tz: "Local",
    rrule: "",
    leadTimes: null,
    status: "open",
    doneAt: null,
    snoozeUntil: null,
    createdAt: new Date(now).toISOString(),
    updatedAt: new Date(now).toISOString(),
    updatedBy: "demo",
    ...extra,
  } as Item;
}

export function installDemo(): void {
  let items: Item[] = [
    item("1", "standup notes", ["team"], -95, { rrule: "FREQ=WEEKLY;BYDAY=MO" }),
    item("2", "check deploy", ["backend"], 180),
    item("3", "read RFC 9110", ["inbox"], null),
    item("4", "fix flaky test", ["backend"], null),
    item("5", "review auth PR with a rather long title that should be truncated at the edge", ["backend", "review"], null),
    item("6", "call the dentist", ["inbox"], 60 * 26),
    item("7", "renew passport", ["home"], 60 * 24 * 4),
    item("8", "write release notes", ["inbox"], null, { status: "done" as Item["status"], doneAt: new Date().toISOString() }),
  ];
  if (new URLSearchParams(location.search).get("demo") === "empty") items = [];
  const tagOf = (it: Item) => (it.tags && it.tags.length ? it.tags : ["inbox"]);
  const eff = (it: Item) => effectiveDue(it, new Date())?.getTime() ?? 0;

  const view = (): View => {
    const open = items.filter((i) => i.status === "open");
    const horizon = Date.now() + 7 * 24 * 3_600_000;
    const upcoming = open.filter((i) => i.kind === "reminder" && eff(i) < horizon).sort((a, b) => eff(a) - eff(b));
    const groups = new Map<string, Item[]>();
    for (const i of open) for (const t of tagOf(i)) groups.set(t, [...(groups.get(t) ?? []), i]);
    const names = [...groups.keys()].sort((a, b) => (a === "inbox" ? -1 : b === "inbox" ? 1 : a.localeCompare(b)));
    return { upcoming, tags: names.map((tag) => ({ tag, items: groups.get(tag)! })), done: items.filter((i) => i.status === "done") };
  };
  const patch = (id: string, p: Partial<Item>) => {
    items = items.map((i) => (i.id === id ? { ...i, ...p } : i));
    window.setTimeout(() => emitLocal(events.itemsChanged), 0);
  };
  const editText = (it: Item) => [it.text, ...(it.tags ?? []).filter((t) => t !== "inbox" || (it.tags ?? []).length > 1).map((t) => `#${t}`)].join(" ");

  Object.assign(api, {
    view: async () => view(),
    done: async (id: string) => patch(id, { status: "done" as Item["status"], doneAt: new Date().toISOString() }),
    reopen: async (id: string) => patch(id, { status: "open" as Item["status"], doneAt: null }),
    deleteItem: async (id: string) => patch(id, { status: "deleted" as Item["status"] }),
    snooze: async (id: string) => patch(id, { snoozeUntil: new Date(Date.now() + 3_600_000).toISOString() }),
    stopRepeating: async (id: string) => patch(id, { rrule: "" }),
    editText: async (id: string) => editText(items.find((i) => i.id === id)!),
    update: async (id: string, input: string) => {
      const tags = [...input.matchAll(/#(\w+)/g)].map((m) => m[1].toLowerCase());
      const text = input.replace(/#\w+/g, "").replace(/\s+/g, " ").trim();
      if (!text) throw new Error("empty input");
      patch(id, { text, tags: tags.length ? tags : ["inbox"] });
      return items.find((i) => i.id === id)!;
    },
    takePendingFocus: async () => new URLSearchParams(location.search).get("focus") ?? "",
    closeWindow: async () => console.info("demo: close window"),
    openSettings: async () => console.info("demo: open settings"),
  });
  installAlertDemo();
  installSettingsDemo();
}

function installAlertDemo(): void {
  document.documentElement.dataset.demo = "";
  const now = Date.now();
  const card = (key: string, text: string, tags: string[], dueInMin: number, extra: Partial<AlertCard> = {}): AlertCard => ({
    key,
    itemId: key,
    text,
    tags,
    dueAt: new Date(now + dueInMin * 60_000).toISOString(),
    lead: 0,
    missed: false,
    recurring: false,
    snoozed: false,
    ...extra,
  });
  let queue: AlertCard[] = [
    card("a4", "renew passport before the trip in March, and book the photo appointment first", ["home"], 60),
    card("a3", "call the dentist", [], -125, { missed: true }),
    card("a2", "standup notes", ["team"], 0, { recurring: true }),
    card("a1", "check deploy", ["backend", "ops"], 10, { lead: 10 * 60e9 }),
  ];
  if (new URLSearchParams(location.search).get("demo") === "one") queue = queue.slice(-1);
  const send = () => window.setTimeout(() => emitLocal(events.alertsShow, [...queue]), 0);
  const drop = (pred: (c: AlertCard) => boolean) => {
    queue = queue.filter((c) => !pred(c));
    send();
  };
  const list = api as unknown as Record<string, (...a: never[]) => Promise<unknown>>;
  const done = list.done;
  const snooze = list.snooze;
  Object.assign(api, {
    pendingAlerts: async () => [...queue],
    dismissAlert: async (key: string) => drop((c) => c.key === key),
    dismissAlerts: async () => drop(() => true),
    resizeAlerts: async (h: number) => console.info(`demo: resize alerts ${h}`),
    openList: async (id: string) => console.info(`demo: open list ${id}`),
    done: async (id: string) => {
      if (id === "a3") throw new Error("item not found");
      await (done as (id: string) => Promise<void>)(id);
      drop((c) => c.itemId === id);
    },
    snooze: async (id: string, preset: string) => {
      await (snooze as (id: string, p: string) => Promise<void>)(id, preset);
      drop((c) => c.itemId === id);
    },
  });
}

function installSettingsDemo(): void {
  let saved: Settings = {
    hotkey: "Ctrl+Shift+Space",
    leadTimes: ["1h0m0s", "10m0s"],
    defaultTime: "09:00",
    nextWeekDay: "monday",
    nextWeekTime: "09:00",
    exportPath: "",
    autostart: true,
  };
  let hotkeyErr = "";
  Object.assign(api, {
    getSettings: async () => ({ ...saved, leadTimes: [...saved.leadTimes] }),
    status: async () => ({ hotkeyErr }),
    setHotkeyRecording: async (on: boolean) => console.info(`demo: hotkey recording ${on}`),
    chooseExportPath: async () => "C:\\Users\\demo\\Documents\\todo",
    saveSettings: async (s: Settings) => {
      const clock = /^([01]\d|2[0-3]):[0-5]\d$/;
      if (!clock.test(s.defaultTime)) throw new Error(`defaultTime: invalid time "${s.defaultTime}", want HH:MM`);
      if (!clock.test(s.nextWeekTime)) throw new Error(`nextWeekTime: invalid time "${s.nextWeekTime}", want HH:MM`);
      if (s.leadTimes.length > 5) throw new Error("leadTimes: at most 5 allowed");
      saved = { ...s, leadTimes: [...s.leadTimes] };
      console.info("demo: saved", saved);
      hotkeyErr = s.hotkey === "Ctrl+Alt+K" ? "Ctrl+Alt+K is already used by another app" : "";
      if (hotkeyErr) throw new Error(`hotkey: ${hotkeyErr}`);
    },
  });
}
