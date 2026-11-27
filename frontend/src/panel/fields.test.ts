import { describe, expect, it, vi } from "vitest";

// Before any import: a zone with an offset (and DST), and the matchMedia
// stub dom.ts reads at load (jsdom has none).
vi.hoisted(() => {
  // Node's process, without pulling @types/node into the app's tsconfig.
  (globalThis as unknown as { process: { env: Record<string, string> } }).process.env.TZ = "America/New_York";
  window.matchMedia = ((q: string) => ({ matches: false, media: q })) as unknown as typeof window.matchMedia;
});

import type { Item } from "../api";
import { dueField, fromLocalInput, personField, repeatOf, sourceField, toLocalInput } from "./fields";

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

describe("dueField snooze note (C1)", () => {
  const hour = 3_600_000;
  const at = (ms: number) => new Date(Date.now() + ms).toISOString();
  const note = (dueIn: number, snoozeIn: number | null): string => {
    const f = dueField(() => {});
    const it = { dueAt: at(dueIn), snoozeUntil: snoozeIn === null ? null : at(snoozeIn) } as Item;
    f.fill(it, true);
    return f.root.querySelector(".pf-note")!.textContent ?? "";
  };

  it("shows a snooze that is ahead and earlier than the due time", () => {
    expect(note(5 * hour, hour)).toMatch(/^Snoozed until /);
  });

  it("hides a snooze that has passed or ends after the due time", () => {
    expect(note(5 * hour, -hour)).toBe("");
    expect(note(hour, 5 * hour)).toBe("");
    expect(note(hour, null)).toBe("");
  });
});

describe("reply fields", () => {
  const reply = (p: Partial<Item>) => ({ reply: true, owedTo: "Priya", source: "Teams", ...p }) as Item;

  it("For saves a changed person on Enter or blur, trimmed, once", () => {
    const saved: string[] = [];
    const f = personField((p) => saved.push(p));
    document.body.append(f.root);
    f.fill(reply({}), true);
    const input = f.root.querySelector("input")!;
    expect(input.value).toBe("Priya");
    expect(f.root.querySelector("label")!.textContent).toBe("For");
    input.focus();
    input.value = "  Sam ";
    input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    expect(saved).toEqual(["Sam"]);
    input.blur(); // nothing new since Enter
    expect(saved).toEqual(["Sam"]);
    input.value = "";
    f.commit(); // the panel's flush on close or switch
    expect(saved).toEqual(["Sam", ""]);
    f.root.remove();
  });

  it("For leaves a person being typed alone on refresh", () => {
    const f = personField(() => {});
    document.body.append(f.root);
    const input = f.root.querySelector("input")!;
    f.fill(reply({}), true);
    input.focus();
    input.value = "Sa";
    f.fill(reply({ owedTo: "Priya" }), false);
    expect(input.value).toBe("Sa");
    f.root.remove();
  });

  it("From shows the source read-only", () => {
    const f = sourceField();
    f.fill(reply({ source: "Outlook" }));
    expect(f.root.querySelector(".pf-label")!.textContent).toBe("From");
    expect(f.root.querySelector(".pf-static")!.textContent).toBe("Outlook");
    expect(f.root.querySelector("input")).toBeNull();
    f.fill(reply({ source: "" }));
    expect(f.root.querySelector(".pf-static")!.textContent).toBe("Unknown");
  });
});
