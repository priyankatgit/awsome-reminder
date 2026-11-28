import { beforeEach, describe, expect, it, vi } from "vitest";
import type { DataFolderInfo, SetupCheck } from "./api";

const h = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} })) as never;
  return {
    info: undefined as unknown as DataFolderInfo,
    picked: "",
    check: undefined as unknown as SetupCheck,
    changes: [] as [string, boolean][],
    change: (async () => {}) as (p: string, c: boolean) => Promise<void>,
    removed: 0,
    remove: (async () => {}) as () => Promise<void>,
    opened: 0,
  };
});
vi.mock("./api", () => ({
  api: {
    chooseFolder: async () => h.picked,
    dataFolder: {
      info: async () => ({ ...h.info }),
      check: async () => ({ ...h.check }),
      change: (p: string, c: boolean) => {
        h.changes.push([p, c]);
        return h.change(p, c);
      },
      removeOldCopy: () => {
        h.removed++;
        return h.remove();
      },
      open: async () => {
        h.opened++;
      },
    },
  },
  errorText: (e: unknown) => (e instanceof Error ? e.message : String(e)),
}));

import { mountDataFolderSection } from "./data-folder-settings";

const tick = () => new Promise((r) => setTimeout(r, 0));
const flush = async () => {
  for (let i = 0; i < 4; i++) await tick();
};

const info = (o: Partial<DataFolderInfo> = {}): DataFolderInfo => ({
  path: "C:\\Users\\me\\AppData\\Roaming\\awesome-reminder",
  usedText: "84 MB",
  freeText: "41.0 GB free",
  previousDir: "",
  previousText: "",
  ...o,
});
const check = (o: Partial<SetupCheck> = {}): SetupCheck => ({
  path: "E:\\Awesome",
  free: 100e9,
  need: 90e6,
  hasData: false,
  network: false,
  cloud: false,
  same: false,
  err: "",
  ...o,
});

let host: HTMLElement;
const btn = (label: string) => [...host.querySelectorAll("button")].find((b) => b.textContent === label && !b.closest("[hidden]")) as HTMLButtonElement | undefined;
const text = () => host.textContent ?? "";

async function mount(i: DataFolderInfo = info()): Promise<void> {
  h.info = i;
  document.body.innerHTML = "<section id='host'></section>";
  host = document.getElementById("host")!;
  mountDataFolderSection(host);
  await flush();
}

async function pick(c: SetupCheck): Promise<void> {
  h.picked = c.path;
  h.check = c;
  btn("Change…")!.click();
  await flush();
}

beforeEach(() => {
  h.changes = [];
  h.change = async () => {};
  h.remove = async () => {};
  h.removed = 0;
  h.opened = 0;
});

describe("data folder section", () => {
  it("shows the path, the usage and the restart note", async () => {
    await mount();
    expect(host.querySelector("#data-folder-title")!.textContent).toBe("Data folder");
    expect(text()).toContain("Changes here restart Awesome Reminder.");
    expect(host.querySelector("#data-folder-path")!.textContent).toBe(info().path);
    expect(host.querySelector("#data-folder-usage")!.textContent).toBe("Uses 84 MB · 41.0 GB free");
  });

  it("opens the folder", async () => {
    await mount();
    btn("Open folder")!.click();
    await flush();
    expect(h.opened).toBe(1);
  });

  it("confirms a move, then restarts into it without confirm", async () => {
    await mount();
    await pick(check());
    expect(text()).toContain("Awesome Reminder will restart to copy your data to E:\\Awesome. Your current data stays where it is as a backup.");
    expect(h.changes).toEqual([]);
    btn("Restart and move")!.click();
    await flush();
    expect(h.changes).toEqual([["E:\\Awesome", false]]);
  });

  it("Cancel closes the confirm without changing anything", async () => {
    await mount();
    await pick(check());
    btn("Cancel")!.click();
    expect(host.querySelector<HTMLElement>("#data-folder-panel")!.hidden).toBe(true);
    expect(h.changes).toEqual([]);
  });

  it("a folder with data offers to use it and confirms the adopt", async () => {
    await mount();
    await pick(check({ hasData: true }));
    expect(text()).toContain("This folder already has Awesome Reminder data.");
    expect(text()).not.toContain("Sync across machines will be off");
    expect(btn("Restart and move")).toBeUndefined();
    btn("Restart and use that data")!.click();
    await flush();
    expect(h.changes).toEqual([["E:\\Awesome", true]]);
  });

  it("warns about network and cloud folders", async () => {
    await mount();
    await pick(check({ network: true, cloud: true }));
    expect(text()).toContain("Network folders can damage the database");
    expect(text()).toContain("Cloud-synced folders can damage the database");
  });

  it("shows a check error inline and no confirm", async () => {
    await mount();
    await pick(check({ err: "Can't write to this folder." }));
    expect(text()).toContain("Can't write to this folder.");
    expect(btn("Restart and move")).toBeUndefined();
  });

  it("says so when the folder is already the data folder", async () => {
    await mount();
    await pick(check({ same: true }));
    expect(text()).toContain("This is already your data folder.");
    expect(btn("Restart and move")).toBeUndefined();
  });

  it("does nothing when the picker is cancelled", async () => {
    await mount();
    h.picked = "";
    btn("Change…")!.click();
    await flush();
    expect(host.querySelector<HTMLElement>("#data-folder-panel")!.hidden).toBe(true);
  });

  it("shows a rejected change in the panel and lets the user try again", async () => {
    await mount();
    h.change = async () => {
      throw new Error("disk exploded");
    };
    await pick(check());
    btn("Restart and move")!.click();
    await flush();
    expect(text()).toContain("disk exploded");
    expect(btn("Change…")!.disabled).toBe(false);
  });

  it("asks again as an adopt when the folder gained data after the check", async () => {
    await mount();
    h.change = async (_p, c) => {
      if (!c) throw new Error("confirm-adopt");
    };
    await pick(check());
    h.check = check({ hasData: true });
    btn("Restart and move")!.click();
    await flush();
    expect(btn("Restart and use that data")).toBeDefined();
  });
});

describe("old copy", () => {
  it("is hidden without a previous folder", async () => {
    await mount();
    expect(host.querySelector<HTMLElement>("#data-folder-old")!.hidden).toBe(true);
    expect(btn("Remove old copy")).toBeUndefined();
  });

  it("shows where it is and how big, and deletes after a confirm", async () => {
    await mount(info({ previousDir: "C:\\Old", previousText: "84 MB" }));
    expect(host.querySelector("#data-folder-old-text")!.textContent).toBe("Old copy at C:\\Old (84 MB)");
    btn("Remove old copy")!.click();
    expect(text()).toContain("Delete the old copy? This can't be undone.");
    expect(h.removed).toBe(0);
    h.info = info();
    btn("Delete")!.click();
    await flush();
    expect(h.removed).toBe(1);
    expect(host.querySelector<HTMLElement>("#data-folder-old")!.hidden).toBe(true);
  });

  it("Cancel keeps the old copy", async () => {
    await mount(info({ previousDir: "C:\\Old", previousText: "84 MB" }));
    btn("Remove old copy")!.click();
    btn("Cancel")!.click();
    expect(h.removed).toBe(0);
    expect(btn("Remove old copy")).toBeDefined();
  });

  it("shows a failed removal and keeps the row", async () => {
    await mount(info({ previousDir: "C:\\Old", previousText: "84 MB" }));
    h.remove = async () => {
      throw new Error("in use");
    };
    btn("Remove old copy")!.click();
    btn("Delete")!.click();
    await flush();
    expect(host.querySelector("#data-folder-error")!.textContent).toBe("in use");
    expect(btn("Remove old copy")).toBeDefined();
  });
});
