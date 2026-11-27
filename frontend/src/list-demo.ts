// Dev-only sample data for previewing the list in a plain browser
// (`npx vite --config frontend/vite.config.ts frontend`, then open
// http://127.0.0.1:9245/?demo#list, or ?demo=empty, or ?demo&focus=4). Loaded only when import.meta.env.DEV
// and ?demo are both set, so production bundles never include it.
import { api, emitLocal, events, type Item, type View } from "./api";

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
  const eff = (it: Item) => new Date(it.snoozeUntil ?? it.dueAt ?? 0).getTime();

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
    getSettings: async () => ({ hotkey: "Ctrl+Shift+Space" }),
    closeWindow: async () => console.info("demo: close window"),
    openSettings: async () => console.info("demo: open settings"),
  });
}
