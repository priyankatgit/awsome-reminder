import { beforeEach, describe, expect, it, vi } from "vitest";

// The module imports the DOM helpers and the Wails API; only its pure helpers run here.
const h = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} })) as never;
  return {
    push: (() => {}) as (s: unknown) => void,
    status: undefined as unknown as () => Promise<unknown>,
    info: undefined as unknown as () => Promise<unknown>,
    now: undefined as unknown as () => Promise<void>,
    connect: undefined as unknown as (u: string) => Promise<unknown>,
    disconnect: undefined as unknown as () => Promise<void>,
  };
});
vi.mock("./api", () => ({
  api: {
    sync: {
      status: () => h.status(),
      info: () => h.info(),
      now: () => h.now(),
      connect: (u: string) => h.connect(u),
      disconnect: () => h.disconnect(),
    },
    openURL: async () => {},
  },
  errorText: String,
  onSyncStatus: (cb: (s: unknown) => void) => {
    h.push = cb;
    return () => {};
  },
}));

import { looksLikeMongoURI, mountSyncSection, storageLine } from "./sync-settings";

describe("looksLikeMongoURI", () => {
  it("accepts srv and plain strings, ignoring surrounding spaces", () => {
    expect(looksLikeMongoURI("mongodb+srv://u:p@cluster0.ab1cd.mongodb.net/")).toBe(true);
    expect(looksLikeMongoURI("mongodb://localhost:27017")).toBe(true);
    expect(looksLikeMongoURI("   mongodb+srv://u:p@h/  ")).toBe(true);
  });
  it("rejects anything else", () => {
    expect(looksLikeMongoURI("https://example.com")).toBe(false);
    expect(looksLikeMongoURI("")).toBe(false);
    expect(looksLikeMongoURI("   ")).toBe(false);
  });
});

describe("storageLine", () => {
  it("shows decimal MB with one decimal place", () => {
    expect(storageLine(12_400_000)).toBe("Images in Atlas: 12.4 MB of 512 MB");
    expect(storageLine(0)).toBe("Images in Atlas: 0.0 MB of 512 MB");
  });
  it("says unknown when the size is not known", () => {
    expect(storageLine(-1)).toBe("Images in Atlas: unknown");
  });
});

const st = (state: string, extra: Record<string, unknown> = {}) => ({
  state, lastSyncAt: "2026-11-28T10:00:00Z", pending: 0, error: "", warnings: [], host: "c0.mongodb.net", ...extra,
});
const info = { connected: true, host: "c0.mongodb.net", secretStore: "Windows Credential Manager", attachmentBytes: 1_000_000 };
const tick = () => new Promise((r) => setTimeout(r, 0));

async function mount(): Promise<HTMLElement> {
  document.body.innerHTML = "<section id='h'></section>";
  const host = document.getElementById("h")!;
  mountSyncSection(host);
  await tick();
  await tick();
  return host;
}
const button = (host: HTMLElement, text: string) =>
  [...host.querySelectorAll("button")].find((b) => b.textContent === text && !b.closest("[hidden]")) as HTMLButtonElement;

describe("section behaviour", () => {
  beforeEach(() => {
    h.status = async () => st("synced");
    h.info = async () => info;
    h.now = async () => {};
    h.connect = async () => st("syncing");
    h.disconnect = async () => {};
  });

  it("keeps focus on Sync now while syncing and ignores a second press", async () => {
    let calls = 0;
    h.now = async () => void calls++;
    const host = await mount();
    const now = button(host, "Sync now");
    now.focus();
    now.click();
    await tick();
    expect(calls).toBe(1);
    h.push(st("syncing"));
    expect(document.activeElement).toBe(now);
    expect(now.disabled).toBe(false);
    expect(now.getAttribute("aria-disabled")).toBe("true");
    now.click();
    await tick();
    expect(calls).toBe(1);
    h.push(st("synced"));
    expect(now.hasAttribute("aria-disabled")).toBe(false);
  });

  it("keeps focus on Connect while connecting and ignores a second press", async () => {
    h.status = async () => st("off");
    h.info = async () => ({ ...info, connected: false });
    let release: (v: unknown) => void = () => {};
    let calls = 0;
    h.connect = () => {
      calls++;
      return new Promise((r) => (release = r));
    };
    const host = await mount();
    const uri = host.querySelector<HTMLInputElement>("#sync-uri")!;
    uri.value = "mongodb+srv://u:p@c0.mongodb.net/";
    uri.dispatchEvent(new Event("input"));
    const connect = button(host, "Connect");
    expect(connect.getAttribute("aria-disabled")).toBeNull();
    connect.focus();
    connect.click();
    await tick();
    expect(button(host, "Connecting…")).toBe(connect);
    expect(document.activeElement).toBe(connect);
    expect(connect.disabled).toBe(false);
    expect(connect.getAttribute("aria-disabled")).toBe("true");
    connect.click();
    expect(calls).toBe(1);
    release(st("syncing"));
  });

  it("shows the connected view from the status when info() rejects", async () => {
    h.info = async () => {
      throw new Error("offline");
    };
    const host = await mount();
    expect(host.querySelector<HTMLElement>("#sync-uri")!.closest<HTMLElement>("div.field")!.hidden).toBe(true);
    expect(button(host, "Sync now")).toBeTruthy();
    expect(host.textContent).toContain("Images in Atlas: unknown");
  });

  it("shows the connected view while a saved string is opened (status off, info connected)", async () => {
    h.status = async () => st("off", { lastSyncAt: null });
    const host = await mount();
    expect(host.querySelector<HTMLElement>("#sync-uri")!.closest<HTMLElement>("div.field")!.hidden).toBe(true);
    expect(button(host, "Sync now")).toBeTruthy();
    expect(button(host, "Connect")).toBeUndefined();
  });

  it("shows a failed Disconnect's error above the connect form it flips to", async () => {
    h.disconnect = async () => {
      throw "access denied";
    };
    const host = await mount();
    button(host, "Disconnect").click();
    h.info = async () => ({ ...info, connected: false });
    // The backend stops sync and reports off before the call fails.
    h.push(st("off", { lastSyncAt: null }));
    const yes = [...host.querySelectorAll<HTMLButtonElement>(".sync-confirm button")].find((b) => b.textContent === "Disconnect")!;
    yes.click();
    await tick();
    await tick();
    const form = host.querySelector<HTMLElement>("#sync-uri")!.closest<HTMLElement>("div.field")!;
    expect(form.hidden).toBe(false);
    const alerts = [...host.querySelectorAll<HTMLElement>("[role=alert]")].filter((a) => !a.hidden && !a.closest("[hidden]"));
    expect(alerts.map((a) => a.textContent)).toEqual([
      "Couldn't remove the saved connection string: access denied. Sync will start again next time the app opens.",
    ]);
    expect(form.contains(alerts[0])).toBe(true);
  });
});
