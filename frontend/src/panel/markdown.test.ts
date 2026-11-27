import { describe, expect, it } from "vitest";
import { renderMarkdown, toggleChecklistLine } from "./markdown";

const ID = "01ARZ3NDEKTSV4RRFFQ69G5FAV";

/** Parses rendered HTML so assertions look at real attributes. */
function dom(html: string): HTMLElement {
  const div = document.createElement("div");
  div.innerHTML = html;
  return div;
}

describe("renderMarkdown", () => {
  it("sanitizes hostile markdown", async () => {
    const html = await renderMarkdown(
      [
        "<img src=x onerror=alert(1)>",
        "",
        "[x](javascript:alert(1))",
        "",
        '<a href="file:///C:/">x</a>',
        "",
        "[y](JaVaScRiPt:alert(1)) [z](file:///C:/Windows) [d](data:text/html,<b>x</b>)",
        "",
        "<script>alert(1)</script><iframe src=https://example.com></iframe>",
        '<b onclick="alert(1)">bold</b> <div style="position:fixed">x</div>',
      ].join("\n"),
    );
    expect(html).not.toMatch(/onerror/i);
    expect(html).not.toMatch(/javascript:/i);
    expect(html).not.toMatch(/file:/i);
    expect(html).not.toMatch(/data:/i);
    expect(html).not.toMatch(/<(script|iframe|b|div)\b/i);
    expect(html).not.toMatch(/onclick|style=/i);
    const root = dom(html);
    expect(root.querySelector("img")).toBeNull();
    for (const a of Array.from(root.querySelectorAll("a"))) expect(a.hasAttribute("href")).toBe(false);
  });

  it("rewrites att: images to /att/", async () => {
    const root = dom(await renderMarkdown(`![shot](att:${ID})`));
    const img = root.querySelector("img");
    expect(img?.getAttribute("src")).toBe(`/att/${ID}`);
    expect(img?.getAttribute("alt")).toBe("shot");
  });

  it("drops images that are not attachments, keeping their alt text", async () => {
    const root = dom(
      await renderMarkdown("![remote](https://example.com/a.png) ![d](data:image/png;base64,AAAA) ![bad](att:../../x)"),
    );
    expect(root.querySelector("img")).toBeNull();
    expect(root.textContent).toContain("remote");
  });

  it("keeps http, https and mailto links", async () => {
    const root = dom(await renderMarkdown("[a](https://example.com/x?y=1) [b](http://e.com) [c](mailto:me@example.com)"));
    const hrefs = Array.from(root.querySelectorAll("a"), (a) => a.getAttribute("href"));
    expect(hrefs).toEqual(["https://example.com/x?y=1", "http://e.com", "mailto:me@example.com"]);
  });

  it("renders task-list items as checkboxes", async () => {
    const root = dom(await renderMarkdown("- [ ] one\n- [x] two"));
    const boxes = Array.from(root.querySelectorAll<HTMLInputElement>("input[type=checkbox]"));
    expect(boxes.map((b) => b.checked)).toEqual([false, true]);
  });
});

describe("toggleChecklistLine", () => {
  it("toggleChecklistLine flips only the nth item", () => {
    const md = "- [ ] a\n- [x] b\n- [ ] c";
    expect(toggleChecklistLine(md, 0)).toBe("- [x] a\n- [x] b\n- [ ] c");
    expect(toggleChecklistLine(md, 1)).toBe("- [ ] a\n- [ ] b\n- [ ] c");
    expect(toggleChecklistLine(md, 2)).toBe("- [ ] a\n- [x] b\n- [x] c");
  });

  it("counts items the way the preview does: nested, ordered, quoted, not in code", () => {
    const md = [
      "```",
      "- [ ] not a task (code)",
      "```",
      "* [ ] first",
      "  - [X] second (nested)",
      "1. [ ] third",
      "> - [ ] fourth",
      "- [] not a task",
    ].join("\n");
    expect(toggleChecklistLine(md, 0)).toContain("* [x] first");
    expect(toggleChecklistLine(md, 1)).toContain("  - [ ] second (nested)");
    expect(toggleChecklistLine(md, 2)).toContain("1. [x] third");
    expect(toggleChecklistLine(md, 3)).toContain("> - [x] fourth");
    expect(toggleChecklistLine(md, 0)).toContain("- [ ] not a task (code)");
    expect(toggleChecklistLine(md, 4)).toBe(md);
  });

  it("keeps Windows line endings", () => {
    expect(toggleChecklistLine("- [ ] a\r\n- [ ] b", 1)).toBe("- [ ] a\r\n- [x] b");
  });
});
