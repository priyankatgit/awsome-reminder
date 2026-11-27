import { describe, expect, it, vi } from "vitest";

// Before any import: a zone with an offset (and DST), and the matchMedia
// stub dom.ts reads at load (jsdom has none).
vi.hoisted(() => {
  // Node's process, without pulling @types/node into the app's tsconfig.
  (globalThis as unknown as { process: { env: Record<string, string> } }).process.env.TZ = "America/New_York";
  window.matchMedia = ((q: string) => ({ matches: false, media: q })) as unknown as typeof window.matchMedia;
});

import { fromLocalInput, repeatOf, toLocalInput } from "./fields";

describe("datetime-local helpers", () => {
  it("runs in a zone with an offset", () => {
    expect(new Date(2026, 10, 30).getTimezoneOffset()).not.toBe(0);
  });

  it("round-trips a due time through local input", () => {
    // 17:00 UTC on 30 Nov is 12:00 in New York (EST, UTC-5).
    expect(toLocalInput("2026-11-30T17:00:00Z")).toBe("2026-11-30T12:00");
    expect(fromLocalInput("2026-11-30T12:00")?.toISOString()).toBe("2026-11-30T17:00:00.000Z");
    // Summer time (EDT, UTC-4).
    const iso = "2026-07-04T13:30:00.000Z";
    expect(fromLocalInput(toLocalInput(iso))?.toISOString()).toBe(iso);
  });

  it("treats empty or incomplete values as no time", () => {
    expect(toLocalInput(null)).toBe("");
    expect(fromLocalInput("")).toBeNull();
    expect(fromLocalInput("2026-11-30T")).toBeNull();
  });
});

describe("repeatOf", () => {
  it("maps the rules SetRepeat writes and flags the rest", () => {
    expect(repeatOf("")).toBe("none");
    expect(repeatOf("FREQ=DAILY")).toBe("daily");
    expect(repeatOf("FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR")).toBe("weekdays");
    expect(repeatOf("FREQ=WEEKLY;BYDAY=TH")).toBe("weekly");
    expect(repeatOf("FREQ=MONTHLY;BYMONTHDAY=1")).toBe("custom");
  });
});
