import { describe, expect, it } from "vitest";
import type { SyncStatus } from "./api";
import { pillModel, relTime, shouldGlow, showMarkers } from "./sync-status";

const now = new Date(2026, 10, 28, 12, 0, 0);
const ago = (ms: number) => new Date(now.getTime() - ms).toISOString();

function st(p: Partial<SyncStatus>): SyncStatus {
  return { state: "synced", lastSyncAt: null, pending: 0, error: "", warnings: [], host: "c.mongodb.net", ...p } as SyncStatus;
}

describe("pillModel", () => {
  it("is null when sync is off", () => {
    expect(pillModel(st({ state: "off" }), now)).toBeNull();
  });
  it("syncing spins", () => {
    expect(pillModel(st({ state: "syncing" }), now)).toEqual({ text: "Syncing…", icon: "refresh", tone: "neutral", spin: true });
  });
  it("synced with nothing waiting", () => {
    expect(pillModel(st({ lastSyncAt: ago(10_000) }), now)).toEqual({ text: "All synced · just now", icon: "cloudCheck", tone: "ok", spin: false });
    expect(pillModel(st({ lastSyncAt: ago(2 * 60_000) }), now)?.text).toBe("All synced · 2 min ago");
    expect(pillModel(st({ lastSyncAt: null }), now)?.text).toBe("All synced");
  });
  it("synced with changes waiting", () => {
    const m = pillModel(st({ pending: 3, lastSyncAt: ago(1000) }), now);
    expect(m).toEqual({ text: "Synced · 3 waiting", icon: "cloud", tone: "neutral", spin: false });
  });
  it("offline", () => {
    expect(pillModel(st({ state: "offline", pending: 1 }), now)).toEqual({ text: "Offline · 1 change waiting", icon: "cloudOff", tone: "muted", spin: false });
    expect(pillModel(st({ state: "offline", pending: 4 }), now)?.text).toBe("Offline · 4 changes waiting");
    expect(pillModel(st({ state: "offline", pending: 0 }), now)?.text).toBe("Offline");
  });
  it("error", () => {
    expect(pillModel(st({ state: "error", error: "bad password" }), now)).toEqual({ text: "Sync error", icon: "cloudAlert", tone: "danger", spin: false });
  });
});

describe("relTime", () => {
  const t = (ms: number) => new Date(now.getTime() - ms);
  it("matches the Go wording", () => {
    expect(relTime(now, t(0))).toBe("just now");
    expect(relTime(now, t(59_000))).toBe("just now");
    expect(relTime(now, t(61_000))).toBe("1 min ago");
    expect(relTime(now, t(59 * 60_000))).toBe("59 min ago");
    expect(relTime(now, t(3 * 3_600_000))).toBe("3 h ago");
    expect(relTime(now, t(48 * 3_600_000))).toBe("on 26 Nov");
    expect(relTime(now, new Date(2026, 0, 2, 9, 0))).toBe("on 2 Jan");
  });
  it("treats a future time as just now", () => {
    expect(relTime(now, new Date(now.getTime() + 5000))).toBe("just now");
  });
});

describe("shouldGlow", () => {
  it("fires once when the last pending change lands", () => {
    expect(shouldGlow(st({ pending: 2 }), st({ pending: 0 }))).toBe(true);
    expect(shouldGlow(st({ pending: 0 }), st({ pending: 0 }))).toBe(false);
    expect(shouldGlow(st({ pending: 2 }), st({ state: "offline", pending: 0 }))).toBe(false);
    expect(shouldGlow(null, st({ pending: 0 }))).toBe(false);
  });
});

describe("showMarkers", () => {
  it("is false only when sync is off", () => {
    expect(showMarkers(st({ state: "off" }))).toBe(false);
    for (const s of ["syncing", "synced", "offline", "error"] as const) expect(showMarkers(st({ state: s }))).toBe(true);
  });
});
