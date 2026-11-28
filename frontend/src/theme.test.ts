import { beforeEach, describe, expect, it } from "vitest";
import { applyTheme, resolveTheme, setThemeChoice, themeChoice, THEME_KEY } from "./theme";

describe("theme", () => {
  let light = false;
  beforeEach(() => {
    localStorage.clear();
    light = false;
    window.matchMedia = ((q: string) => ({
      get matches() {
        return q.includes("light") && light;
      },
      media: q,
      addEventListener() {},
      removeEventListener() {},
    })) as never;
  });

  it("is black by default, even on a light OS", () => {
    light = true;
    expect(themeChoice()).toBe("black");
    expect(applyTheme()).toBe("dark");
    expect(document.documentElement.dataset.theme).toBe("dark");
  });

  it("Match Windows follows the OS", () => {
    expect(resolveTheme("system", true)).toBe("light");
    expect(resolveTheme("system", false)).toBe("dark");
    expect(resolveTheme("black", true)).toBe("dark");
  });

  it("saves the choice and applies it; black clears the key", () => {
    light = true;
    setThemeChoice("system");
    expect(localStorage.getItem(THEME_KEY)).toBe("system");
    expect(document.documentElement.dataset.theme).toBe("light");
    setThemeChoice("black");
    expect(localStorage.getItem(THEME_KEY)).toBeNull();
    expect(document.documentElement.dataset.theme).toBe("dark");
  });

  it("an unknown stored value is black", () => {
    localStorage.setItem(THEME_KEY, "neon");
    expect(themeChoice()).toBe("black");
  });
});
