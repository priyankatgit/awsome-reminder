import { describe, expect, it } from "vitest";
import { MAX_INPUT, checkImageType, checkInputSize, compressTarget, pickQuality } from "./images";

describe("compressTarget", () => {
  it("compressTarget picks quality step", () => {
    expect(compressTarget(4000, 3000)).toEqual({ w: 1600, h: 1200 });
    expect(compressTarget(3000, 4000)).toEqual({ w: 1200, h: 1600 });
    expect(compressTarget(800, 600)).toEqual({ w: 800, h: 600 });
    expect(compressTarget(1600, 900)).toEqual({ w: 1600, h: 900 });
    expect(compressTarget(10000, 1)).toEqual({ w: 1600, h: 1 });
    expect(pickQuality(700_000)).toBe(0.6);
    expect(pickQuality(500_000)).toBe(0.8);
  });
});

describe("input guards", () => {
  it("rejects >20MB", () => {
    expect(MAX_INPUT).toBe(20 * 1024 * 1024);
    expect(checkInputSize(25 * 1024 * 1024)).toBe("Image too large (max 20 MB)");
    expect(checkInputSize(MAX_INPUT + 1)).toBe("Image too large (max 20 MB)");
    expect(checkInputSize(MAX_INPUT)).toBeNull();
    expect(checkInputSize(8 * 1024 * 1024)).toBeNull();
  });

  it("accepts PNG, JPEG, WebP, GIF and BMP only", () => {
    for (const t of ["image/png", "image/jpeg", "image/webp", "image/gif", "image/bmp"]) expect(checkImageType(t)).toBeNull();
    expect(checkImageType("image/svg+xml")).toBe("Use a PNG, JPEG, WebP, GIF or BMP image");
    expect(checkImageType("text/plain")).toBe("Use a PNG, JPEG, WebP, GIF or BMP image");
  });
});
