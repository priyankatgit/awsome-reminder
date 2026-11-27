// Dev-only sample data for previewing the views in a plain browser
// (`npx vite --config frontend/vite.config.ts frontend`, then open
// http://127.0.0.1:9245/?demo#list, or ?demo=empty, or ?demo&focus=4;
// ?demo#alert, ?demo=one#alert; ?demo#settings, where saving the hotkey
// Ctrl+Alt+K fails as "already used"; ?demo#capture, with a toy parser that
// knows "tomorrow", "5pm", #tags and !priority tokens). Click a row in
// ?demo#list to try the detail panel; its saves take a moment, like the real
// ones. Item 2's description has a checklist, links and a demo image; item 4
// carries hostile Markdown, to check that the preview stays inert. There is no
// Go /att/ route here, so attachments live in memory as data:/blob: URLs.
// Loaded only when import.meta.env.DEV and ?demo are both set, so production
// bundles never include it.
import { api, emitLocal, events, type AlertCard, type Item, type Preview, type Settings, type View } from "./api";
import { effectiveDue } from "./item-row";

/** A demo attachment (ULID-shaped id) and the in-memory images by id. */
const DEMO_ATT = "01JDEMQ" + "0".repeat(19);
const demoImages = new Map<string, string>([
  [
    DEMO_ATT,
    "data:image/svg+xml," +
      encodeURIComponent(
        '<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="600"><defs><linearGradient id="g" x2="1" y2="1">' +
          '<stop offset="0" stop-color="#1e3a8a"/><stop offset="1" stop-color="#15803d"/></linearGradient></defs>' +
          '<rect width="1200" height="600" fill="url(#g)"/><polyline points="0,480 200,420 400,450 600,300 800,330 1000,180 1200,220" ' +
          'fill="none" stroke="#fff" stroke-width="8"/><text x="40" y="80" fill="#fff" font-size="48" font-family="sans-serif">demo image</text></svg>',
      ),
  ],
]);

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
    priority: "",
    description: "",
    ...extra,
  } as Item;
}

export function installDemo(): void {
  let items: Item[] = [
    item("1", "standup notes", ["team"], -95, { rrule: "FREQ=WEEKLY;BYDAY=MO" }),
    item("2", "check deploy", ["backend"], 180, {
      priority: "high",
      description: [
        "Steps:",
        "- [ ] watch the canary",
        "- [x] check error rate",
        "",
        "Dashboards: [Grafana](https://grafana.example.com), or [mail ops](mailto:ops@example.com)",
        "",
        `![canary graph](att:${DEMO_ATT})`,
      ].join("\n"),
    }),
    item("3", "read RFC 9110", ["inbox"], null, { priority: "low" }),
    item("4", "fix flaky test", ["backend"], null, {
      priority: "medium",
      description: [
        "Fails about 1 in 20 runs on CI.",
        "",
        "## Sanitizer check (all inert)",
        "",
        '<img src=x onerror=alert(1)> [js link](javascript:alert(1)) <a href="file:///C:/">file link</a>',
        "![remote image](https://example.com/a.png) <script>alert(1)</script>",
        "",
        "```",
        "let flaky = true",
        "```",
      ].join("\n"),
    }),
    item("5", "review auth PR with a rather long title that should be truncated at the edge", ["backend", "review"], null),
    item("6", "call the dentist", ["inbox"], 60 * 26),
    item("7", "renew passport", ["home"], 60 * 24 * 4),
    item("8", "write release notes", ["inbox"], null, { status: "done" as Item["status"], doneAt: new Date().toISOString() }),
  ];
  if (new URLSearchParams(location.search).get("demo") === "empty") items = [];
  const tagOf = (it: Item) => (it.tags && it.tags.length ? it.tags : ["inbox"]);
  const rank: Record<string, number> = { high: 0, medium: 1, low: 2 };
  const byPriority = (a: Item, b: Item) => (rank[a.priority] ?? 3) - (rank[b.priority] ?? 3);
  const eff = (it: Item) => effectiveDue(it, new Date())?.getTime() ?? 0;

  const view = (): View => {
    const open = items.filter((i) => i.status === "open");
    const horizon = Date.now() + 7 * 24 * 3_600_000;
    const upcoming = open.filter((i) => i.kind === "reminder" && eff(i) < horizon).sort((a, b) => eff(a) - eff(b));
    const groups = new Map<string, Item[]>();
    for (const i of open) for (const t of tagOf(i)) groups.set(t, [...(groups.get(t) ?? []), i]);
    const names = [...groups.keys()].sort((a, b) => (a === "inbox" ? -1 : b === "inbox" ? 1 : a.localeCompare(b)));
    // Like Go's View: tag groups sort by priority (stable), Upcoming keeps time order.
    const tags = names.map((tag) => ({ tag, items: [...groups.get(tag)!].sort(byPriority) }));
    return { upcoming, tags, done: items.filter((i) => i.status === "done"), waiting: [], pending: [] };
  };
  const patch = (id: string, p: Partial<Item>) => {
    items = items.map((i) => (i.id === id ? { ...i, ...p, updatedAt: new Date().toISOString() } : i));
    window.setTimeout(() => emitLocal(events.itemsChanged), 0);
  };
  const editText = (it: Item) => [it.text, ...(it.tags ?? []).filter((t) => t !== "inbox" || (it.tags ?? []).length > 1).map((t) => `#${t}`)].join(" ");

  Object.assign(api, {
    view: async () => view(),
    done: async (id: string) => patch(id, { status: "done" as Item["status"], doneAt: new Date().toISOString() }),
    reopen: async (id: string) => patch(id, { status: "open" as Item["status"], doneAt: null }),
    deleteItem: async (id: string) => patch(id, { status: "deleted" as Item["status"] }),
    undelete: async (id: string) => {
      const it = items.find((i) => i.id === id);
      const status = it?.doneAt && !it.rrule ? "done" : "open";
      patch(id, { status: status as Item["status"] });
    },
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
  installPanelDemo(() => items, patch);
  installAlertDemo();
  installSettingsDemo();
  installCaptureDemo();
}

/** Stand-ins for the detail panel's setters, with Go's validation messages. */
function installPanelDemo(all: () => Item[], patch: (id: string, p: Partial<Item>) => void): void {
  const codes = ["SU", "MO", "TU", "WE", "TH", "FR", "SA"];
  const weekly = (due: string) => `FREQ=WEEKLY;BYDAY=${codes[new Date(due).getDay()]}`;
  const get = (id: string) => {
    const it = all().find((i) => i.id === id);
    if (!it) throw new Error("item not found");
    return it;
  };
  const slow = () => new Promise((r) => window.setTimeout(r, 150)); // a save takes a moment
  // Stands in for Go's /att/<id> route: preview images get their in-memory URL.
  new MutationObserver(() => {
    for (const img of Array.from(document.querySelectorAll<HTMLImageElement>('img[src^="/att/"]'))) {
      const url = demoImages.get(img.getAttribute("src")!.slice(5));
      if (url) img.src = url;
    }
  }).observe(document.body, { childList: true, subtree: true });
  const set = async (id: string, p: Partial<Item>) => {
    get(id);
    patch(id, p);
    return get(id);
  };
  Object.assign(api, {
    itemDetail: async (id: string) => ({ item: get(id), attachments: [] }),
    setText: async (id: string, text: string) => {
      await slow();
      if (!text.trim()) throw new Error("empty input");
      return set(id, { text: text.trim() });
    },
    setDescription: async (id: string, md: string) => {
      await slow();
      return set(id, { description: md });
    },
    setPriority: async (id: string, p: string) => {
      if (!["", "low", "medium", "high"].includes(p)) throw new Error("unknown priority");
      return set(id, { priority: p });
    },
    setDue: async (id: string, due: Date | null) => {
      const it = get(id);
      if (!due) return set(id, { dueAt: null, kind: "todo" as Item["kind"], rrule: "", snoozeUntil: null });
      const dueAt = due.toISOString();
      const rrule = /^FREQ=WEEKLY;BYDAY=[A-Z]{2}$/.test(it.rrule) ? weekly(dueAt) : it.rrule;
      return set(id, { dueAt, kind: "reminder" as Item["kind"], rrule, snoozeUntil: null });
    },
    setRepeat: async (id: string, r: string) => {
      const it = get(id);
      if (r === "none") return set(id, { rrule: "" });
      if (!it.dueAt) throw new Error("Set a due time first");
      const rules: Record<string, string> = { daily: "FREQ=DAILY", weekdays: "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR", weekly: weekly(it.dueAt) };
      if (!rules[r]) throw new Error("unknown repeat");
      return set(id, { rrule: rules[r] });
    },
    setTags: async (id: string, tags: string[]) => {
      const out = [...new Set(tags.map((t) => t.trim().replace(/^#/, "").toLowerCase()).filter(Boolean))];
      if (out.some((t) => !/^[a-z][a-z0-9_-]*$/.test(t))) throw new Error("tags start with a letter and use a-z, 0-9, - or _");
      return set(id, { tags: out.length ? out : ["inbox"] });
    },
    addAttachment: async (itemId: string, webp: Blob) => {
      get(itemId);
      await new Promise((r) => window.setTimeout(r, 800)); // shows the placeholder
      if (webp.type !== "image/webp") throw new Error("image must be WebP");
      if (webp.size > 2 * 1024 * 1024) throw new Error("image too large after compression (max 2 MB)");
      const id = "01JDEMQ" + String(demoImages.size).padStart(19, "0");
      demoImages.set(id, URL.createObjectURL(webp));
      console.info(`demo: stored ${id}, ${webp.size} bytes`);
      return { id, itemId, mime: "image/webp", bytes: webp.size };
    },
    openURL: async (url: string) => {
      if (!/^(https?:|mailto:)/i.test(url)) throw new Error("unsupported link");
      console.info(`demo: open ${url}`);
    },
    widenList: async (w: number) => console.info(`demo: widen list to ${w}`),
    restoreListWidth: async () => console.info("demo: restore list width"),
  });
}

/** A toy stand-in for the Go parser: enough to drive the capture preview. */
function installCaptureDemo(): void {
  const prio: Record<string, string> = { "!high": "high", "!h": "high", "!!!": "high", "!medium": "medium", "!med": "medium", "!m": "medium", "!low": "low", "!l": "low" };
  const fmt = new Intl.DateTimeFormat("en-GB", { weekday: "short", day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", hour12: false });
  Object.assign(api, {
    preview: async (input: string): Promise<Preview> => {
      const words = input.split(/\s+/).filter(Boolean);
      const priority = words.map((w) => prio[w.toLowerCase()]).find(Boolean) ?? "";
      const tags = words.filter((w) => /^#\w+$/.test(w)).map((w) => w.slice(1).toLowerCase());
      const tomorrow = words.some((w) => w.toLowerCase() === "tomorrow");
      const clock = words.map((w) => /^(\d{1,2})(am|pm)$/i.exec(w)).find(Boolean);
      let dueAt: string | null = null;
      if (tomorrow || clock) {
        const d = new Date();
        if (tomorrow) d.setDate(d.getDate() + 1);
        d.setHours(clock ? (Number(clock[1]) % 12) + (clock[2].toLowerCase() === "pm" ? 12 : 0) : 9, 0, 0, 0);
        dueAt = d.toISOString();
      }
      const text = words.filter((w) => !prio[w.toLowerCase()] && !w.startsWith("#")).join(" ");
      return {
        kind: (dueAt ? "reminder" : "todo") as Preview["kind"],
        text,
        tags: tags.length ? tags : ["inbox"],
        dueAt,
        rrule: "",
        priority,
        human: dueAt ? fmt.format(new Date(dueAt)).replace(/ at /, ", ") : "",
        alerts: dueAt ? ["1h before", "10m before", "at time"] : null,
        warnings: null,
      };
    },
    capture: async (input: string) => console.info(`demo: capture ${input}`),
    hideCapture: async () => console.info("demo: hide capture"),
    resizeCapture: async () => undefined,
  });
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
    kind: "",
    person: "",
    source: "",
    waitingSince: "0001-01-01T00:00:00Z",
    people: [],
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
    showPendingCount: true,
    replyHotkey: "Ctrl+Alt+R",
    workDays: ["monday", "tuesday", "wednesday", "thursday", "friday"],
    endOfDay: "17:30",
    endOfDaySweep: true,
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
