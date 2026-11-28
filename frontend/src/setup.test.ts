import { beforeEach, describe, expect, it, vi } from "vitest";
import type { SetupCheck, SetupDone, SetupInfo, SetupProgress } from "./api";

const h = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} })) as never;
  return {
    info: undefined as unknown as SetupInfo,
    picked: "",
    check: undefined as unknown as SetupCheck,
    chooseErr: "" as string,
    calls: [] as string[],
    chosen: [] as [string, string, boolean][],
    onProgress: undefined as undefined | ((p: SetupProgress) => void),
    onDone: undefined as undefined | ((d: SetupDone) => void),
  };
});
vi.mock("./api", () => ({
  api: {
    setup: {
      info: async () => h.info,
      check: async () => h.check,
      choose: async (kind: string, path: string, confirm: boolean) => {
        h.chosen.push([kind, path, confirm]);
        if (h.chooseErr && !confirm) throw new Error(h.chooseErr);
      },
      runPendingMove: async () => void h.calls.push("run"),
      cancelMove: async () => void h.calls.push("cancel"),
      abandonMove: async () => void h.calls.push("abandon"),
      retry: async () => h.info,
      quit: async () => void h.calls.push("quit"),
      onProgress: (cb: (p: SetupProgress) => void) => ((h.onProgress = cb), () => undefined),
      onDone: (cb: (d: SetupDone) => void) => ((h.onDone = cb), () => undefined),
    },
    chooseFolder: async () => h.picked,
  },
  errorText: (e: unknown) => (e instanceof Error ? e.message : String(e)),
}));

import { formatBytes, mountSetup, percent } from "./setup";

const info = (o: Partial<SetupInfo> = {}): SetupInfo => ({
  state: "fresh",
  root: "",
  reason: "",
  existingDir: "",
  moveFrom: "",
  moveTo: "",
  needText: "",
  options: [
    { kind: "standard", label: "Standard location", path: "C:\\Users\\me\\AppData\\Roaming\\awesome-reminder", free: 4e10, freeText: "37.3 GB free" },
    { kind: "beside", label: "Next to the app", path: "D:\\app\\Data", free: 3e11, freeText: "279.4 GB free" },
  ],
  ...o,
});
const existing = () =>
  info({
    state: "existing",
    existingDir: "C:\\Users\\me\\AppData\\Roaming\\awesome-reminder",
    needText: "41 MB",
    options: [{ ...info().options[0], label: "Keep it where it is" }, info().options[1]],
  });
const check = (o: Partial<SetupCheck> = {}): SetupCheck => ({
  path: "E:\\Notes\\Awesome Reminder", free: 5e10, need: 1e7, hasData: false, network: false, cloud: false, same: false, err: "", ...o,
});

const flush = async () => {
  for (let i = 0; i < 5; i++) await new Promise((r) => setTimeout(r, 0));
};
const root = () => document.getElementById("app")!;
const $ = <T extends HTMLElement>(sel: string) => root().querySelector<T>(sel)!;
const byText = (text: string) => [...root().querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === text)!;

async function mount(i: SetupInfo): Promise<void> {
  h.info = i;
  mountSetup(root());
  await flush();
}

/** Browse a folder and wait for its check to show. */
async function browse(c: SetupCheck): Promise<void> {
  h.picked = c.path;
  h.check = c;
  byText("Browse").click();
  await flush();
}

beforeEach(() => {
  document.body.innerHTML = '<div id="app"></div>';
  h.calls = [];
  h.chosen = [];
  h.chooseErr = "";
  h.picked = "";
  h.onProgress = h.onDone = undefined;
});

describe("helpers", () => {
  it("formats sizes like the Go side", () => {
    expect(formatBytes(41 * 1024 * 1024)).toBe("41 MB");
    expect(formatBytes(40 * 1024 ** 3)).toBe("40.0 GB");
  });
  it("computes a percentage, 0 while the total is unknown", () => {
    expect(percent({ step: "database", done: 1, total: 4 })).toBe(25);
    expect(percent({ step: "database", done: 0, total: 0 })).toBe(0);
  });
});

describe("options", () => {
  it("shows the existing state with the first row selected and the notice", async () => {
    await mount(existing());
    expect(root().textContent).toContain("Where should Awesome Reminder keep its data?");
    expect(root().textContent).toContain("about 41 MB");
    expect($(".setup-notice").textContent).toContain("You already have data in");
    const first = $<HTMLInputElement>("#setup-opt-standard");
    expect(first.checked).toBe(true);
    expect($(".setup-opt-label").textContent).toBe("Keep it where it is");
    expect($("[role=radiogroup]")).toBeTruthy();
  });

  it("shows the fresh state without the notice", async () => {
    await mount(info());
    expect(root().querySelector(".setup-notice")).toBeNull();
    expect($(".setup-opt-label").textContent).toBe("Standard location");
    expect($<HTMLButtonElement>("button[type=submit]").disabled).toBe(false);
  });

  it("shows a check error and disables Continue", async () => {
    await mount(info());
    await browse(check({ err: "Can't write to this folder." }));
    expect($(".setup-msgs").textContent).toContain("Can't write to this folder.");
    expect($<HTMLButtonElement>("button[type=submit]").disabled).toBe(true);
    expect($<HTMLInputElement>("#setup-opt-other").checked).toBe(true);
  });

  it("shows the path and free space of the browsed folder", async () => {
    await mount(info());
    await browse(check());
    expect(root().textContent).toContain("E:\\Notes\\Awesome Reminder");
    expect(root().textContent).toContain("46.6 GB free");
  });

  it("warns about a cloud folder and still lets you continue", async () => {
    await mount(info());
    await browse(check({ cloud: true }));
    expect($(".setup-msgs").textContent).toContain("Cloud-synced folders can damage the database");
    expect($<HTMLButtonElement>("button[type=submit]").disabled).toBe(false);
  });

  it("warns about a network folder", async () => {
    await mount(info());
    await browse(check({ network: true }));
    expect($(".setup-msgs").textContent).toContain("Network folders can damage the database");
  });

  it("cancelling the picker changes nothing", async () => {
    await mount(info());
    h.picked = "";
    byText("Browse").click();
    await flush();
    expect($<HTMLInputElement>("#setup-opt-standard").checked).toBe(true);
  });

  it("asks to confirm adopting a folder that has data, then confirms", async () => {
    await mount(existing());
    await browse(check({ hasData: true }));
    h.chooseErr = "confirm-adopt";
    $<HTMLButtonElement>("button[type=submit]").click();
    await flush();
    expect(h.chosen[0]).toEqual(["other", "E:\\Notes\\Awesome Reminder", false]);
    expect(root().textContent).toContain("This folder already has Awesome Reminder data.");
    expect(root().textContent).toContain("Your other data stays where it is.");
    expect(root().textContent).toContain("Sync across machines will be off until you connect again.");
    byText("Use that data").click();
    await flush();
    expect(h.chosen[1]).toEqual(["other", "E:\\Notes\\Awesome Reminder", true]);
  });

  it("Back from the confirm panel returns to the options", async () => {
    await mount(existing());
    await browse(check({ hasData: true }));
    h.chooseErr = "confirm-adopt";
    $<HTMLButtonElement>("button[type=submit]").click();
    await flush();
    byText("Back").click();
    expect(root().querySelector("[role=radiogroup]")).toBeTruthy();
    expect($<HTMLInputElement>("#setup-opt-other").checked).toBe(true);
  });

  it("shows a rejection from choose inline", async () => {
    await mount(info());
    h.chooseErr = "Can't write to this folder.";
    $<HTMLButtonElement>("button[type=submit]").click();
    await flush();
    expect($(".setup-msgs").textContent).toContain("Can't write to this folder.");
  });

  it("an existing-data choice that goes through shows the move", async () => {
    await mount(existing());
    await browse(check());
    $<HTMLButtonElement>("button[type=submit]").click();
    await flush();
    expect(root().querySelector("[role=progressbar]")).toBeTruthy();
    expect(root().textContent).toContain("E:\\Notes\\Awesome Reminder");
  });
});

describe("a move", () => {
  async function startMove(): Promise<void> {
    await mount(existing());
    await browse(check());
    $<HTMLButtonElement>("button[type=submit]").click();
    await flush();
  }

  it("follows progress events", async () => {
    await startMove();
    h.onProgress!({ step: "attachments", done: 1, total: 4 });
    const bar = $("[role=progressbar]");
    expect(bar.getAttribute("aria-valuenow")).toBe("25");
    expect(bar.getAttribute("aria-label")).toBe("Copying images…");
    h.onProgress!({ step: "checking", done: 3, total: 4 });
    expect(bar.getAttribute("aria-valuenow")).toBe("75");
    expect(bar.getAttribute("aria-label")).toBe("Checking the copy…");
  });

  it("offers Try again after a failure, and Try again re-sends the choice", async () => {
    await startMove();
    h.onDone!({ ok: false, error: "disk full" });
    expect(root().textContent).toContain("disk full");
    byText("Try again").click();
    await flush();
    expect(h.chosen.at(-1)).toEqual(["other", "E:\\Notes\\Awesome Reminder", false]);
  });

  it("Cancel while running stops the move and returns to the options", async () => {
    await startMove();
    byText("Cancel").click();
    expect(h.calls).toContain("cancel");
    h.onDone!({ ok: false, error: "context canceled" });
    expect(root().querySelector("[role=radiogroup]")).toBeTruthy();
    expect(root().textContent).not.toContain("context canceled");
  });
});

describe("unavailable", () => {
  const unavailable = () => info({ state: "unavailable", root: "E:\\Notes\\Awesome Reminder", reason: "awesome.db is missing", options: [] });

  it("focuses Retry and Quit quits", async () => {
    await mount(unavailable());
    expect(root().textContent).toContain("Your data folder isn't available");
    expect(root().textContent).toContain("awesome.db is missing");
    expect(document.activeElement).toBe(byText("Retry"));
    byText("Quit").click();
    expect(h.calls).toContain("quit");
  });

  it("confirm-fresh then Start with an empty list confirms", async () => {
    await mount(unavailable());
    h.chooseErr = "confirm-fresh";
    h.picked = "E:\\Notes\\Awesome Reminder";
    h.check = check();
    byText("Choose another folder…").click();
    await flush();
    expect(root().textContent).toContain("Start with an empty list here?");
    byText("Start with an empty list").click();
    await flush();
    expect(h.chosen.at(-1)).toEqual(["other", "E:\\Notes\\Awesome Reminder", true]);
  });
});

describe("pending move", () => {
  it("runs the move once on mount and shows where it goes", async () => {
    await mount(info({ state: "move", root: "C:\\old", moveFrom: "C:\\old", moveTo: "E:\\New" }));
    expect(h.calls.filter((c) => c === "run")).toHaveLength(1);
    expect(root().textContent).toContain("Moving your data");
    expect(root().textContent).toContain("E:\\New");
    expect(root().querySelector("[role=progressbar]")).toBeTruthy();
  });

  it("Cancel after a failed move abandons it", async () => {
    await mount(info({ state: "move", root: "C:\\old", moveFrom: "C:\\old", moveTo: "E:\\New" }));
    h.onDone!({ ok: false, error: "target gone" });
    byText("Cancel").click();
    expect(h.calls).toContain("abandon");
  });
});
