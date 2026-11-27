import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Item, View } from "./api";

// The list imports the Wails API; a fake stands in. jsdom has no matchMedia,
// ResizeObserver or scrollIntoView.
const h = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} })) as never;
  (window as unknown as { ResizeObserver: unknown }).ResizeObserver = class {
    observe(): void {}
    disconnect(): void {}
  };
  Element.prototype.scrollIntoView = function () {};
  return { view: null as unknown };
});

const api = vi.hoisted(() => ({
  view: async () => h.view,
  done: vi.fn(async (_id: string) => {}),
  reopen: vi.fn(async (_id: string) => {}),
  dropReply: vi.fn(async (_id: string) => {}),
  deleteItem: vi.fn(async (_id: string) => {}),
  snooze: vi.fn(async () => {}),
  getSettings: async () => ({ hotkey: "Ctrl+Shift+Space" }),
  takePendingFocus: async () => "",
  openSettings: async () => {},
  closeWindow: async () => {},
  sync: { status: async () => ({ state: "off", warnings: [], pending: 0, lastSyncAt: null, error: "", host: "" }), now: async () => {} },
}));

vi.mock("./api", () => ({
  api,
  errorText: (e: unknown) => (e instanceof Error ? e.message : String(e)),
  events: { itemsChanged: "items:changed", listFocus: "list:focus" },
  on: () => () => {},
  onSyncStatus: () => () => {},
}));

const celebrate = vi.hoisted(() => vi.fn());
vi.mock("./motion", async (orig) => ({ ...(await orig<typeof import("./motion")>()), celebrate }));

import { mountList } from "./list";

const MIN = 60_000;
const REMINDER = "reminder" as Item["kind"];

function item(id: string, text: string, extra: Partial<Item> = {}): Item {
  const now = Date.now();
  return {
    id,
    kind: "todo",
    text,
    tags: ["inbox"],
    dueAt: null,
    tz: "Local",
    rrule: "",
    leadTimes: null,
    status: "open",
    doneAt: null,
    snoozeUntil: null,
    createdAt: new Date(now).toISOString(),
    updatedAt: new Date(now).toISOString(),
    updatedBy: "test",
    priority: "",
    description: "",
    reply: false,
    owedTo: "",
    source: "",
    replyOutcome: "",
    ...extra,
  } as Item;
}

function reply(id: string, owedTo: string, source: string, ageMin: number, extra: Partial<Item> = {}): Item {
  return item(id, `Reply to ${owedTo || "someone"}`, {
    reply: true,
    owedTo,
    source,
    createdAt: new Date(Date.now() - ageMin * MIN).toISOString(),
    ...extra,
  });
}

function view(v: Partial<View>): View {
  return { upcoming: [], tags: [], done: [], waiting: [], pending: [], ...v };
}

const tick = () => new Promise((r) => window.setTimeout(r, 0));

async function mount(v: View): Promise<HTMLElement> {
  h.view = v;
  document.body.innerHTML = '<div id="app"></div>';
  const root = document.querySelector<HTMLElement>("#app")!;
  mountList(root);
  await tick();
  await tick();
  return root;
}

const sections = (root: HTMLElement) => Array.from(root.querySelectorAll<HTMLElement>(".sec"));
const rowsOf = (root: HTMLElement, id: string) => Array.from(root.querySelectorAll<HTMLElement>(`.row[data-id="${id}"]`));

describe("Waiting on you", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders first, titled with its count, with a subheading per person", async () => {
    const r1 = reply("r1", "Priya", "Teams", 5);
    const r2 = reply("r2", "Priya", "Outlook", 60 * 30);
    const r3 = reply("r3", "", "", 120);
    const root = await mount(
      view({
        upcoming: [item("a", "standup", { kind: REMINDER, dueAt: new Date(Date.now() + 60 * MIN).toISOString() })],
        tags: [{ tag: "inbox", items: [item("b", "read RFC")] }],
        waiting: [
          { person: "Priya", items: [r1, r2] },
          { person: "", items: [r3] },
        ],
      }),
    );
    const secs = sections(root);
    expect(secs[0].dataset.key).toBe("waiting");
    expect(secs[0].querySelector(".sec-head")!.textContent).toBe("Waiting on you (3)");
    expect(secs[1].dataset.key).toBe("upcoming");
    const subs = Array.from(secs[0].querySelectorAll(".sec-sub")).map((s) => s.textContent);
    expect(subs).toEqual(["Priya", "Someone"]);
    // Each person's rows form their own list, labelled by the subheading.
    const lists = Array.from(secs[0].querySelectorAll<HTMLElement>('[role="list"]'));
    expect(lists.map((l) => Array.from(l.querySelectorAll(".row")).map((r) => (r as HTMLElement).dataset.id))).toEqual([["r1", "r2"], ["r3"]]);
    expect(document.getElementById(lists[0].getAttribute("aria-labelledby")!)!.textContent).toBe("Priya");
  });

  it("rows show the source and the age; an age over 24 h is stale", async () => {
    const root = await mount(
      view({ waiting: [{ person: "Priya", items: [reply("r1", "Priya", "Teams", 5), reply("r2", "Priya", "", 60 * 30)] }] }),
    );
    const [fresh] = rowsOf(root, "r1");
    expect(fresh.querySelector(".row-src")!.textContent).toBe("Teams");
    expect(fresh.querySelector(".row-age")!.textContent).toBe("5 min");
    expect(fresh.querySelector(".row-age")!.classList.contains("is-stale")).toBe(false);
    const [old] = rowsOf(root, "r2");
    expect(old.querySelector(".row-src")).toBeNull();
    expect(old.querySelector(".row-age")!.classList.contains("is-stale")).toBe(true);
  });

  it("a reply never shows twice, even if a tag group or Today also lists it", async () => {
    const r1 = reply("r1", "Priya", "Teams", 5, { kind: REMINDER, dueAt: new Date(Date.now() + 30 * MIN).toISOString() });
    const root = await mount(
      view({
        upcoming: [r1],
        tags: [{ tag: "inbox", items: [r1, item("b", "read RFC")] }],
        waiting: [{ person: "Priya", items: [r1] }],
      }),
    );
    expect(rowsOf(root, "r1")).toHaveLength(1);
    expect(rowsOf(root, "r1")[0].closest<HTMLElement>(".sec")!.dataset.key).toBe("waiting");
    // Today / Upcoming had only the reply: it goes away.
    expect(sections(root).map((s) => s.dataset.key)).toEqual(["waiting", "tag:inbox"]);
  });

  it("the row menu offers No reply needed, which drops the reply", async () => {
    const root = await mount(view({ waiting: [{ person: "Priya", items: [reply("r1", "Priya", "Teams", 5)] }] }));
    const [row] = rowsOf(root, "r1");
    row.querySelector<HTMLElement>(".row-more")!.click();
    await tick();
    const items = Array.from(document.querySelectorAll<HTMLElement>('[role="menuitem"]'));
    const drop = items.find((i) => i.textContent === "No reply needed")!;
    expect(drop).toBeTruthy();
    drop.click();
    expect(api.dropReply).toHaveBeenCalledWith("r1");
  });

  it("the checkbox marks a reply replied (Done)", async () => {
    const root = await mount(view({ waiting: [{ person: "Priya", items: [reply("r1", "Priya", "Teams", 5)] }] }));
    const check = rowsOf(root, "r1")[0].querySelector<HTMLElement>(".row-check")!;
    expect(check.getAttribute("aria-label")).toBe("Mark replied: Reply to Priya");
    check.click();
    await tick();
    expect(api.done).toHaveBeenCalledWith("r1");
  });

  it("a dropped reply in Done reads no reply needed", async () => {
    const dropped = reply("r9", "Sam", "Slack", 60, { status: "done" as Item["status"], doneAt: new Date().toISOString(), replyOutcome: "dropped" });
    const replied = reply("r8", "Sam", "Slack", 60, { status: "done" as Item["status"], doneAt: new Date().toISOString(), replyOutcome: "replied" });
    localStorage.setItem("awesome-reminder.list.doneOpen", "1");
    const root = await mount(view({ done: [dropped, replied] }));
    expect(rowsOf(root, "r9")[0].querySelector(".row-dropped")!.textContent).toBe("no reply needed");
    expect(rowsOf(root, "r8")[0].querySelector(".row-dropped")).toBeNull();
    localStorage.removeItem("awesome-reminder.list.doneOpen");
  });

  it("an open reply keeps the list out of its empty state", async () => {
    const root = await mount(view({ waiting: [{ person: "", items: [reply("r1", "", "", 5)] }] }));
    expect(root.querySelector<HTMLElement>("#list-empty")!.hidden).toBe(true);
  });
});

describe("Today is clear", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });
  const soon = () => new Date(Date.now() + 30 * MIN).toISOString();

  it("an undated reply does not count as due today", async () => {
    const a = item("a", "standup", { kind: REMINDER, dueAt: soon() });
    const root = await mount(view({ upcoming: [a], tags: [{ tag: "inbox", items: [a] }], waiting: [{ person: "", items: [reply("r1", "", "", 5)] }] }));
    rowsOf(root, "a")[0].querySelector<HTMLElement>(".row-check")!.click();
    await tick();
    await tick();
    expect(celebrate).toHaveBeenCalledTimes(1);
  });

  it("a reply due today does count", async () => {
    const a = item("a", "standup", { kind: REMINDER, dueAt: soon() });
    const r1 = reply("r1", "Priya", "", 5, { kind: REMINDER, dueAt: soon() });
    const root = await mount(view({ upcoming: [a], tags: [{ tag: "inbox", items: [a] }], waiting: [{ person: "Priya", items: [r1] }] }));
    rowsOf(root, "a")[0].querySelector<HTMLElement>(".row-check")!.click();
    await tick();
    await tick();
    expect(celebrate).not.toHaveBeenCalled();
  });

  it("completing the last reply due today clears the day; an undated one does not", async () => {
    const r1 = reply("r1", "Priya", "", 5, { kind: REMINDER, dueAt: soon() });
    const r2 = reply("r2", "Sam", "", 5);
    const root = await mount(view({ waiting: [{ person: "Priya", items: [r1] }, { person: "Sam", items: [r2] }] }));
    rowsOf(root, "r2")[0].querySelector<HTMLElement>(".row-check")!.click();
    await tick();
    await tick();
    expect(celebrate).not.toHaveBeenCalled();
    rowsOf(root, "r1")[0].querySelector<HTMLElement>(".row-check")!.click();
    await tick();
    await tick();
    expect(celebrate).toHaveBeenCalledTimes(1);
  });
});

// Last: every mounted list keeps its document keydown listener, so keyboard
// tests must run after the others have stopped caring about selection.
describe("keyboard across the Waiting section", () => {
  it("arrows walk from Waiting into the next section; Space replies, Delete removes", async () => {
    vi.clearAllMocks();
    const b = item("b", "read RFC");
    const root = await mount(
      view({
        tags: [{ tag: "inbox", items: [b] }],
        waiting: [
          { person: "Priya", items: [reply("r1", "Priya", "Teams", 5)] },
          { person: "Sam", items: [reply("r2", "Sam", "Slack", 5)] },
        ],
      }),
    );
    const key = (k: string) => document.body.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true }));
    key("ArrowDown");
    expect(document.activeElement).toBe(rowsOf(root, "r1")[0]);
    key("ArrowDown");
    expect(document.activeElement).toBe(rowsOf(root, "r2")[0]);
    key("ArrowDown");
    expect(document.activeElement).toBe(rowsOf(root, "b")[0]);
    key("Home");
    expect(document.activeElement).toBe(rowsOf(root, "r1")[0]);
    key(" ");
    await tick();
    expect(api.done).toHaveBeenCalledWith("r1");
    key("End");
    key("ArrowUp");
    expect(document.activeElement).toBe(rowsOf(root, "r2")[0]);
    key("Delete");
    await tick();
    expect(api.deleteItem).toHaveBeenCalledWith("r2");
  });
});
