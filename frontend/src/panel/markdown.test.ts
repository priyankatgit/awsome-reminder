import { beforeAll, describe, expect, it } from "vitest";
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
  beforeAll(() => renderMarkdown("")); // it uses marked's lexer, loaded by the first render

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

  /** Ticking box i in the rendered preview must flip box i, and only it. */
  async function expectMatchesPreview(md: string, want: number): Promise<void> {
    const states = async (src: string) =>
      Array.from(dom(await renderMarkdown(src)).querySelectorAll<HTMLInputElement>("input[type=checkbox]"), (b) => b.checked);
    const before = await states(md);
    expect(before.length).toBe(want);
    for (let i = 0; i < before.length; i++) {
      const after = await states(toggleChecklistLine(md, i));
      expect(after).toEqual(before.map((v, j) => (j === i ? !v : v)));
    }
  }

  it("ignores task-like lines marked doesn't treat as tasks", async () => {
    // A raw HTML block (stripped from the preview), then a real item.
    await expectMatchesPreview("<div>\n- [ ] inside html\n</div>\n\n- [ ] real", 1);
    // 4-space indented code outside a list.
    await expectMatchesPreview("para\n\n    - [ ] code\n\n- [ ] real", 1);
    // "2." can't interrupt a paragraph, so this is paragraph text.
    await expectMatchesPreview("some text\n2. [ ] not a list\n\n- [x] real", 1);
    // A fence line with an info string doesn't close the fence.
    await expectMatchesPreview("```\n```js\n- [ ] still code\n```\n- [ ] real", 1);
    // Same checkbox text twice in one item, tasks in a quote and nested.
    await expectMatchesPreview("- [ ] a [ ] b\n> - [x] q\n- [ ] p\n  - [ ] n", 4);
  });

  it("returns md unchanged for an index that isn't there", () => {
    expect(toggleChecklistLine("- [ ] a", 1)).toBe("- [ ] a");
    expect(toggleChecklistLine("- [ ] a", -1)).toBe("- [ ] a");
  });
});
