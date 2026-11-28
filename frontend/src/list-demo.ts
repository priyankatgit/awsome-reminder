// Dev-only sample data for previewing the views in a plain browser
// (`npx vite --config frontend/vite.config.ts frontend`, then open
// http://127.0.0.1:9245/?demo#list, or ?demo=empty, or ?demo&focus=4;
// ?demo#alert, ?demo=one#alert, ?demo=replies#alert (nudge and sweep cards
// only); ?demo#settings, where saving the hotkey
// Ctrl+Alt+K fails as "already used"; ?demo#capture, with a toy parser that
// knows "tomorrow", "5pm", #tags and !priority tokens, and a bottom-left
// select that opens it normally or in reply mode; ?demo&reply#capture opens
// it in reply mode at once). Click a row in
// ?demo#list to try the detail panel; its saves take a moment, like the real
// ones. Item 2's description has a checklist, links and a demo image; item 4
// carries hostile Markdown, to check that the preview stays inert. Replies
// owed (r1-r5) fill "Waiting on you": Priya twice (one over 24 h), Sam with a
// due time today, one owed to nobody; r6 sits in Done as "no reply needed". There is no
// Go /att/ route here, so attachments live in memory as data:/blob: URLs.
// Loaded only when import.meta.env.DEV and ?demo are both set, so production
// bundles never include it.
import { api, emitLocal, events, type AlertCard, type Item, type Preview, type ReplyMeta, type ReplyPrefill, type Settings, type View } from "./api";
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
    reply: false,
    owedTo: "",
    source: "",
    replyOutcome: "",
    ...extra,
  } as Item;
}

/** A reply owed, captured ageMin ago. */
function replyItem(id: string, text: string, owedTo: string, source: string, ageMin: number, dueInMin: number | null = null, extra: Partial<Item> = {}): Item {
  const created = new Date(Date.now() - ageMin * 60_000).toISOString();
  return item(id, text, ["inbox"], dueInMin, { reply: true, owedTo, source, createdAt: created, updatedAt: created, ...extra });
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
    replyItem("r2", 'Reply to Priya: "budget numbers for Q1?"', "Priya", "Outlook", 60 * 30, null, {
      description: "> Hi, could you send the budget numbers for Q1 before the review?",
    }),
    replyItem("r1", 'Reply to Priya: "can you check the deploy before 5?"', "Priya", "Microsoft Teams (work or school)", 125),
    replyItem("r4", "Reply to Sam: confirm the venue", "Sam", "Slack", 50, 240),
    replyItem("r3", 'Reply: "is the staging DB back?"', "", "Chrome", 12),
    replyItem("r6", "Reply to Ops channel: rota swap", "Ops channel", "Slack", 60 * 5, null, {
      status: "done" as Item["status"],
      doneAt: new Date().toISOString(),
      replyOutcome: "dropped",
    }),
  ];
  if (new URLSearchParams(location.search).get("demo") === "empty") items = [];
  const tagOf = (it: Item) => (it.tags && it.tags.length ? it.tags : ["inbox"]);
  const rank: Record<string, number> = { high: 0, medium: 1, low: 2 };
  const byPriority = (a: Item, b: Item) => (rank[a.priority] ?? 3) - (rank[b.priority] ?? 3);
  const eff = (it: Item) => effectiveDue(it, new Date())?.getTime() ?? 0;

  let pendingIds: string[] = [];
  // Like Go's groupReplies: by person (case-insensitive), oldest first, nobody last.
  const created = (it: Item) => new Date(it.createdAt).getTime();
  const waiting = (open: Item[]) => {
    const by = new Map<string, Item[]>();
    for (const it of [...open].filter((i) => i.reply).sort((a, b) => created(a) - created(b))) {
      const k = it.owedTo.trim().toLowerCase();
      by.set(k, [...(by.get(k) ?? []), it]);
    }
    return [...by.entries()]
      .sort(([a, x], [b, y]) => (a === "") !== (b === "") ? (a === "" ? 1 : -1) : created(x[0]) - created(y[0]))
      .map(([, list]) => ({ person: list[0].owedTo.trim(), items: list }));
  };
  const view = (): View => {
    const all = items.filter((i) => i.status === "open");
    const open = all.filter((i) => !i.reply);
    const horizon = Date.now() + 7 * 24 * 3_600_000;
    const upcoming = open.filter((i) => i.kind === "reminder" && eff(i) < horizon).sort((a, b) => eff(a) - eff(b));
    const groups = new Map<string, Item[]>();
    for (const i of open) for (const t of tagOf(i)) groups.set(t, [...(groups.get(t) ?? []), i]);
    const names = [...groups.keys()].sort((a, b) => (a === "inbox" ? -1 : b === "inbox" ? 1 : a.localeCompare(b)));
    // Like Go's View: tag groups sort by priority (stable), Upcoming keeps time order.
    const tags = names.map((tag) => ({ tag, items: [...groups.get(tag)!].sort(byPriority) }));
    return { upcoming, tags, done: items.filter((i) => i.status === "done"), waiting: waiting(all), pending: [...pendingIds] };
  };
  const patch = (id: string, p: Partial<Item>) => {
    items = items.map((i) => (i.id === id ? { ...i, ...p, updatedAt: new Date().toISOString() } : i));
    window.setTimeout(() => emitLocal(events.itemsChanged), 0);
  };
  const editText = (it: Item) => [it.text, ...(it.tags ?? []).filter((t) => t !== "inbox" || (it.tags ?? []).length > 1).map((t) => `#${t}`)].join(" ");

  Object.assign(api, {
    view: async () => view(),
    done: async (id: string) => {
      const reply = items.find((i) => i.id === id)?.reply;
      patch(id, { status: "done" as Item["status"], doneAt: new Date().toISOString(), replyOutcome: reply ? "replied" : "" });
    },
    reopen: async (id: string) => patch(id, { status: "open" as Item["status"], doneAt: null, replyOutcome: "" }),
    dropReply: async (id: string) => patch(id, { status: "done" as Item["status"], doneAt: new Date().toISOString(), replyOutcome: "dropped" }),
    snoozeReplies: async () => {
      const until = new Date(Date.now() + 16 * 3_600_000).toISOString();
      for (const it of items.filter((i) => i.reply && i.status === "open")) patch(it.id, { snoozeUntil: until });
    },
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
  installSyncDemo((ids) => {
    pendingIds = ids;
  });
  installPanelDemo(() => items, patch);
  installAlertDemo();
  installSettingsDemo();
  installCaptureDemo();
}

/**
 * A state switcher for the sync pill (bottom-left select; ?demo#list):
 * every status the pill and the row markers can show. Switching from
 * "Synced, 3 waiting" to "All synced" plays the one glow.
 */
function installSyncDemo(setPending: (ids: string[]) => void): void {
  const ago = (ms: number) => new Date(Date.now() - ms).toISOString();
  const base = { error: "", warnings: [] as string[], host: "cluster0.ab1cd.mongodb.net" };
  type Demo = { label: string; ids: string[]; status: () => Record<string, unknown> };
  const states: Demo[] = [
    { label: "Off", ids: [], status: () => ({ ...base, state: "off", lastSyncAt: null, pending: 0, host: "" }) },
    { label: "Syncing", ids: ["2"], status: () => ({ ...base, state: "syncing", lastSyncAt: ago(120_000), pending: 1 }) },
    { label: "Synced, 0 waiting", ids: [], status: () => ({ ...base, state: "synced", lastSyncAt: ago(10_000), pending: 0 }) },
    { label: "Synced, 3 waiting", ids: ["2", "3", "5"], status: () => ({ ...base, state: "synced", lastSyncAt: ago(120_000), pending: 3 }) },
    { label: "Offline, 0 waiting", ids: [], status: () => ({ ...base, state: "offline", lastSyncAt: ago(3 * 3_600_000), pending: 0, error: "No network connection" }) },
    { label: "Offline, 1 waiting", ids: ["4"], status: () => ({ ...base, state: "offline", lastSyncAt: ago(3 * 3_600_000), pending: 1, error: "No network connection" }) },
    { label: "Offline, 4 waiting", ids: ["1", "2", "5", "6"], status: () => ({ ...base, state: "offline", lastSyncAt: ago(30 * 3_600_000), pending: 4, error: "No network connection" }) },
    { label: "Error", ids: ["3"], status: () => ({ ...base, state: "error", lastSyncAt: ago(600_000), pending: 1, error: "Authentication failed: check the user name and password in your connection string." }) },
    {
      label: "Synced with warnings",
      ids: [],
      status: () => ({
        ...base,
        state: "synced",
        lastSyncAt: ago(60_000),
        pending: 0,
        warnings: [
          "This computer's clock is 3 min off from your cluster; edits may lose to older ones.",
          "This Atlas user can reach other databases on your cluster. Limit it to readWrite on awesome_reminder (see the setup guide).",
        ],
      }),
    },
  ];
  let current = states[location.hash === "#settings" ? 0 : 3];
  const apply = (d: Demo) => {
    current = d;
    setPending(d.ids);
    emitLocal(events.syncStatus, d.status());
  };
  Object.assign(api.sync, {
    status: async () => {
      setPending(current.ids);
      return { ...current.status(), warnings: (current.status().warnings as string[]) ?? [] };
    },
    // Settings preview (?demo#settings): a "good" string is any mongodb+srv://
    // one; one containing "bad" fails like Go's check.
    connect: async (u: string) => {
      await new Promise((r) => window.setTimeout(r, 600));
      if (u.includes("bad")) throw new Error("Authentication failed: check the user name and password in your connection string.");
      current = states[2];
      return current.status();
    },
    disconnect: async () => {
      current = states[0];
      window.setTimeout(() => emitLocal(events.syncStatus, current.status()), 0);
    },
    info: async () => ({
      connected: current !== states[0],
      host: current === states[0] ? "" : "cluster0.ab1cd.mongodb.net",
      secretStore: location.search.includes("fallback") ? "Encrypted file on this computer" : "Windows Credential Manager",
      attachmentBytes: location.search.includes("unknown") ? -1 : 12_400_000,
    }),
    now: async () => {
      apply(states[1]);
      await new Promise((r) => window.setTimeout(r, 800));
      apply(states[2]);
    },
  });
  if (location.hash !== "#list") return;
  const select = document.createElement("select");
  select.setAttribute("aria-label", "Demo: sync state");
  select.style.cssText =
    "position:fixed;left:var(--space-2);bottom:var(--space-2);z-index:20;padding:var(--space-1);background:var(--surface);color:var(--fg);border:var(--hairline) solid var(--border);border-radius:var(--radius-chip);font:inherit;font-size:var(--text-small)";
  states.forEach((d, i) => select.append(new Option(d.label, String(i), false, d === current)));
  select.addEventListener("change", () => apply(states[Number(select.value)]));
  document.body.append(select);
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
    setOwedTo: async (id: string, person: string) => {
      await slow();
      return set(id, { owedTo: person.trim() });
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
    // A line containing "fail" fails, to show the inline error.
    captureReply: async (input: string, meta: ReplyMeta) => {
      if (input.includes("fail")) throw new Error("demo: could not save the reply");
      console.info(`demo: capture reply ${input}`, meta);
    },
    hideCapture: async () => console.info("demo: hide capture"),
    resizeCapture: async () => undefined,
  });
  installReplyDemo();
}

/**
 * Reply mode samples for ?demo#capture: a bottom-left select fires
 * capture:open or reply:open with a sample prefill. "Keep text" acts like the
 * reply key pressed while the box is open: type something first.
 */
function installReplyDemo(): void {
  if (location.hash !== "#capture") return;
  const quote = "can you check the deploy before 5? The staging numbers look off.";
  const none = { line: "", owedTo: "", source: "", quote: "", grabbed: false, needsAccess: false, keepText: false };
  const samples: { label: string; prefill?: ReplyPrefill }[] = [
    { label: "Demo: open the box…" },
    { label: "Capture (normal)" },
    { label: "Reply: Priya, from Teams", prefill: { ...none, line: `Reply to Priya: "${quote.slice(0, 40)}…"`, owedTo: "Priya", source: "Teams", quote, grabbed: true } },
    { label: "Reply: nobody guessed", prefill: { ...none, line: `Reply: "${quote.slice(0, 40)}…"`, source: "Chrome", quote, grabbed: true } },
    { label: "Reply: needs Accessibility (macOS)", prefill: { ...none, line: "Reply to Sam", owedTo: "Sam", source: "Slack", needsAccess: true } },
    { label: "Reply: keep text (box already open)", prefill: { ...none, keepText: true } },
  ];
  const fire = (i: number) => {
    const p = samples[i].prefill;
    if (p) emitLocal(events.replyOpen, p);
    else if (i > 0) emitLocal(events.captureOpen);
  };
  const select = document.createElement("select");
  select.setAttribute("aria-label", "Demo: open the capture box");
  select.style.cssText =
    "position:fixed;left:var(--space-2);bottom:var(--space-2);z-index:20;padding:var(--space-1);background:var(--surface);color:var(--fg);border:var(--hairline) solid var(--border);border-radius:var(--radius-chip);font:inherit;font-size:var(--text-small)";
  samples.forEach((d, i) => select.append(new Option(d.label, String(i), i === 0, i === 0)));
  select.addEventListener("change", () => {
    fire(Number(select.value));
    select.value = "0";
  });
  document.body.append(select);
  if (new URLSearchParams(location.search).has("reply")) window.setTimeout(() => fire(2), 0);
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
  const ago = (min: number) => new Date(now - min * 60_000).toISOString();
  const nudges: AlertCard[] = [
    card("r3|nudge", 'Reply: "is the staging DB back?"', [], 0, { itemId: "r3", kind: "nudge", source: "Chrome", waitingSince: ago(12) }),
    card("r1|nudge", 'Reply to Priya: "can you check the deploy before 5?"', [], 0, { itemId: "r1", kind: "nudge", person: "Priya", source: "Teams", waitingSince: ago(125) }),
    card("sweep|today", "", [], 0, { itemId: "", kind: "sweep", people: ["Priya", "Sam", "Someone"] }),
  ];
  const mode = new URLSearchParams(location.search).get("demo");
  if (mode === "one") queue = queue.slice(-1);
  else if (mode === "replies") queue = nudges;
  else queue = [...queue, ...nudges.slice(1)]; // newest first: the sweep, then Priya's nudge
  const send = () => window.setTimeout(() => emitLocal(events.alertsShow, [...queue]), 0);
  const drop = (pred: (c: AlertCard) => boolean) => {
    queue = queue.filter((c) => !pred(c));
    send();
  };
  const list = api as unknown as Record<string, (...a: never[]) => Promise<unknown>>;
  const done = list.done;
  const snooze = list.snooze;
  const snoozeReplies = list.snoozeReplies;
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
    snoozeReplies: async () => {
      await (snoozeReplies as () => Promise<void>)();
      drop((c) => c.kind !== "");
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
    status: async () => ({ hotkeyErr, replyHotkeyErr: "" }),
    setHotkeyRecording: async (on: boolean) => console.info(`demo: hotkey recording ${on}`),
    chooseExportPath: async () => "C:\\Users\\demo\\Documents\\todo",
    saveSettings: async (s: Settings) => {
      const clock = /^([01]\d|2[0-3]):[0-5]\d$/;
      if (!clock.test(s.defaultTime)) throw new Error(`defaultTime: invalid time "${s.defaultTime}", want HH:MM`);
      if (!clock.test(s.nextWeekTime)) throw new Error(`nextWeekTime: invalid time "${s.nextWeekTime}", want HH:MM`);
      if (s.leadTimes.length > 5) throw new Error("leadTimes: at most 5 allowed");
      if (s.workDays.length === 0) throw new Error("workDays: pick at least one working day");
      if (!clock.test(s.endOfDay)) throw new Error(`endOfDay: invalid time "${s.endOfDay}", want HH:MM`);
      if (s.replyHotkey === s.hotkey) throw new Error("Pick a different key from the capture hotkey");
      saved = { ...s, leadTimes: [...s.leadTimes] };
      console.info("demo: saved", saved);
      hotkeyErr = s.hotkey === "Ctrl+Alt+K" ? "Ctrl+Alt+K is already used by another app" : "";
      if (hotkeyErr) throw new Error(`hotkey: ${hotkeyErr}`);
      return "";
    },
  });
}
