import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AlertCard } from "./api";

const h = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} })) as never;
  (window as unknown as { ResizeObserver: unknown }).ResizeObserver = class {
    observe(): void {}
    disconnect(): void {}
  };
  return { queue: [] as unknown[] };
});

const api = vi.hoisted(() => ({
  pendingAlerts: async () => h.queue,
  done: vi.fn(async (_id: string) => {}),
  snooze: vi.fn(async (_id: string, _preset: string) => {}),
  snoozeReplies: vi.fn(async () => {}),
  openList: vi.fn(async (_id: string) => {}),
  dismissAlert: vi.fn(async (_key: string) => {}),
  dismissAlerts: vi.fn(async () => {}),
  resizeAlerts: vi.fn(async () => {}),
  getSettings: async () => ({}),
}));

vi.mock("./api", () => ({
  api,
  errorText: (e: unknown) => (e instanceof Error ? e.message : String(e)),
  events: { alertsShow: "alerts:show" },
  on: () => () => {},
}));

import { mountAlert, whenLabel } from "./alert";

const HOUR = 3_600_000;

function card(p: Partial<AlertCard>): AlertCard {
  return {
    key: "k",
    itemId: "i",
    text: "a card",
    dueAt: new Date().toISOString(),
    lead: 0,
    missed: false,
    tags: [],
    recurring: false,
    snoozed: false,
    kind: "",
    person: "",
    source: "",
    waitingSince: "0001-01-01T00:00:00Z",
    people: [],
    ...p,
  };
}

const tick = () => new Promise((r) => window.setTimeout(r, 0));

async function mount(queue: AlertCard[]): Promise<HTMLElement> {
  h.queue = queue;
  document.body.innerHTML = '<div id="app"></div>';
  const root = document.querySelector<HTMLElement>("#app")!;
  mountAlert(root);
  await tick();
  return root;
}

const buttons = (node: Element) => Array.from(node.querySelectorAll<HTMLButtonElement>(".alert-actions button"));

describe("nudge card", () => {
  beforeEach(() => vi.clearAllMocks());

  it("reads waiting <age> · <person>", () => {
    const now = new Date();
    const c = card({ kind: "nudge", person: "Priya", waitingSince: new Date(now.getTime() - 2 * HOUR).toISOString() });
    expect(whenLabel(c, now).text).toBe("waiting 2 h · Priya");
    expect(whenLabel({ ...c, person: "" }, now).text).toBe("waiting 2 h · Someone");
  });

  it("offers Replied, which completes the item", async () => {
    const root = await mount([card({ key: "n1", itemId: "r1", text: "Reply to Priya", kind: "nudge", person: "Priya", waitingSince: new Date(Date.now() - 2 * HOUR).toISOString() })]);
    const node = root.querySelector<HTMLElement>(".alert-card")!;
    expect(node.querySelector(".alert-when")!.textContent).toBe("waiting 2 h · Priya");
    const [primary] = buttons(node);
    expect(primary.textContent).toBe("Replied");
    primary.click();
    await tick();
    expect(api.done).toHaveBeenCalledWith("r1");
  });
});

describe("nudge card snooze", () => {
  beforeEach(() => vi.clearAllMocks());

  it("Priya's 2 h nudge: Snooze, then 1 hour, snoozes the reply", async () => {
    const root = await mount([card({ key: "n1", itemId: "r1", text: "Reply to Priya", kind: "nudge", person: "Priya", waitingSince: new Date(Date.now() - 2 * HOUR).toISOString() })]);
    const node = root.querySelector<HTMLElement>(".alert-card")!;
    buttons(node).find((b) => b.textContent === "Snooze")!.click();
    const hour = Array.from(node.querySelectorAll<HTMLButtonElement>(".alert-snooze button")).find((b) => b.textContent === "1 hour")!;
    hour.click();
    await tick();
    expect(api.snooze).toHaveBeenCalledWith("r1", "1h");
    expect(node.querySelector<HTMLElement>(".alert-error")!.hidden).toBe(true);
  });
});

describe("sweep card", () => {
  beforeEach(() => vi.clearAllMocks());
  const sweep = (people: string[]) => card({ key: "sweep|2026-12-02", itemId: "", text: "", kind: "sweep", people });

  it("lists who is still waiting", async () => {
    const root = await mount([sweep(["Priya", "Sam", "Ops channel"])]);
    expect(root.querySelector(".alert-text")!.textContent).toBe("3 people are still waiting on you — Priya, Sam, Ops channel");
    const one = await mount([sweep(["Priya"])]);
    expect(one.querySelector(".alert-text")!.textContent).toBe("1 person is still waiting on you — Priya");
    expect(buttons(one.querySelector(".alert-card")!).map((b) => b.textContent)).toEqual(["Open list", "Tomorrow"]);
  });

  it("Tomorrow snoozes every reply, then the card closes", async () => {
    const root = await mount([sweep(["Priya", "Someone"])]);
    const tomorrow = buttons(root.querySelector(".alert-card")!).find((b) => b.textContent === "Tomorrow")!;
    tomorrow.click();
    await tick();
    await tick();
    expect(api.snoozeReplies).toHaveBeenCalledTimes(1);
    expect(api.done).not.toHaveBeenCalled();
    expect(root.querySelector(".alert-card")).toBeNull();
  });

  it("Open list opens the list unfocused and dismisses the card", async () => {
    const root = await mount([sweep(["Priya"])]);
    const open = buttons(root.querySelector(".alert-card")!).find((b) => b.textContent === "Open list")!;
    open.click();
    await tick();
    await tick();
    expect(api.openList).toHaveBeenCalledWith("");
    expect(api.dismissAlert).toHaveBeenCalledWith("sweep|2026-12-02");
    expect(root.querySelector(".alert-card")).toBeNull();
  });
});
