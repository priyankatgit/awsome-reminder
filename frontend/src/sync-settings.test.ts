import { describe, expect, it, vi } from "vitest";

// The module imports the DOM helpers and the Wails API; only its pure helpers run here.
vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} })) as never;
});
vi.mock("./api", () => ({ api: {}, errorText: String, onSyncStatus: () => () => {} }));

import { looksLikeMongoURI, storageLine } from "./sync-settings";

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
