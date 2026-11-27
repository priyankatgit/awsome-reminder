import { describe, expect, it } from "vitest";
import { ageLabel, isStale } from "./waiting";

const at = (iso: string) => new Date(iso); // local time
const MIN = 60_000;
const HOUR = 60 * MIN;

describe("ageLabel", () => {
  const now = at("2026-12-03T11:00"); // Thursday
  const ago = (ms: number) => new Date(now.getTime() - ms);

  it.each([
    ["20 s", ago(20_000), "1 min"],
    ["5 min", ago(5 * MIN), "5 min"],
    ["59 min", ago(59 * MIN + 59_000), "59 min"],
    ["1 h", ago(HOUR), "1 h"],
    ["2 h 40 min", ago(2 * HOUR + 40 * MIN), "2 h"],
    ["23 h 59 min, over midnight", ago(24 * HOUR - MIN), "23 h"],
    ["25 h, the day before", at("2026-12-02T10:00"), "yesterday"],
    ["35 h, still the day before", at("2026-12-02T00:00"), "yesterday"],
    ["35 h 30 min, two days back by date", at("2026-12-01T23:30"), "2 days"],
    ["3 days", at("2026-11-30T10:00"), "3 days"],
    ["12 days", at("2026-11-21T10:00"), "12 days"],
  ])("%s → %s", (_name, created, want) => {
    expect(ageLabel(created, now)).toBe(want);
  });

  it("a creation time in the future (clock skew) reads as 1 min", () => {
    expect(ageLabel(new Date(now.getTime() + 5 * MIN), now)).toBe("1 min");
  });
});

describe("isStale", () => {
  const now = at("2026-12-03T11:00");
  const ago = (ms: number) => new Date(now.getTime() - ms);

  it.each([
    ["2 h", ago(2 * HOUR), false],
    ["exactly 24 h", ago(24 * HOUR), false],
    ["24 h 1 min", ago(24 * HOUR + MIN), true],
    ["3 days", ago(72 * HOUR), true],
  ])("%s → %s", (_name, created, want) => {
    expect(isStale(created, now)).toBe(want);
  });
});
