import { beforeEach, describe, expect, it, vi } from "vitest";
import type { SyncStatus } from "./api";

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} })) as never;
});

let push: (s: SyncStatus) => void = () => {};
const now = vi.fn(async () => {});
vi.mock("./api", () => ({
  api: {
    sync: { status: vi.fn(async () => ({})), now: () => now() },
    openSettings: vi.fn(async () => {}),
  },
  onSyncStatus: (cb: (s: SyncStatus) => void) => {
    push = cb;
    return () => {};
  },
}));

import { mountSyncPill } from "./sync-pill";

const status = (p: Partial<SyncStatus>): SyncStatus =>
  ({ state: "synced", lastSyncAt: null, pending: 1, error: "", warnings: [], host: "h.mongodb.net", ...p }) as SyncStatus;

describe("sync popover keyboard path", () => {
  beforeEach(() => {
    document.body.innerHTML = '<div id="slot"></div>';
    now.mockClear();
  });

  it("keeps focus on Sync now while syncing, and Esc closes and refocuses the pill", () => {
    const slot = document.querySelector<HTMLElement>("#slot")!;
    mountSyncPill(slot, { onStatus: () => {} });
    push(status({}));
    const pill = slot.querySelector<HTMLButtonElement>(".sync-pill")!;
    pill.click();
    const nowBtn = slot.querySelector<HTMLButtonElement>(".sync-pop .btn-primary")!;
    expect(document.activeElement).toBe(nowBtn);

    nowBtn.click();
    expect(now).toHaveBeenCalledTimes(1);
    push(status({ state: "syncing" }));
    expect(document.activeElement).toBe(nowBtn);
    expect(slot.contains(document.activeElement)).toBe(true);
    nowBtn.click(); // ignored while busy
    expect(now).toHaveBeenCalledTimes(1);

    const outer = vi.fn();
    document.addEventListener("keydown", outer);
    nowBtn.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    document.removeEventListener("keydown", outer);
    expect(outer).not.toHaveBeenCalled();
    expect(slot.querySelector<HTMLElement>(".sync-pop")!.hidden).toBe(true);
    expect(pill.getAttribute("aria-expanded")).toBe("false");
    expect(document.activeElement).toBe(pill);
  });
});
