import { describe, expect, it } from "vitest";
import {
  bold,
  bulletList,
  checklist,
  code,
  heading,
  imageMarkdown,
  insertImage,
  insertText,
  italic,
  link,
  replaceMarker,
  type TextState,
} from "./format";

/** A state from a template where [ and ] mark the selection (| for a caret). */
function at(tpl: string): TextState {
  if (tpl.includes("|")) {
    const i = tpl.indexOf("|");
    return { value: tpl.replace("|", ""), selStart: i, selEnd: i };
  }
  const a = tpl.indexOf("[");
  const b = tpl.indexOf("]") - 1;
  return { value: tpl.replace("[", "").replace("]", ""), selStart: a, selEnd: b };
}

const selected = (s: TextState) => s.value.slice(s.selStart, s.selEnd);

describe("bold", () => {
  it("wraps the selection with **", () => {
    const s = bold(at("say [hello] now"));
    expect(s.value).toBe("say **hello** now");
    expect(selected(s)).toBe("hello");
  });

  it("inserts **** with the caret in the middle when nothing is selected", () => {
    const s = bold(at("ab|cd"));
    expect(s.value).toBe("ab****cd");
    expect(s.selStart).toBe(4);
    expect(s.selEnd).toBe(4);
  });
});

describe("italic", () => {
  it("wraps the selection with *", () => {
    expect(italic(at("[x]")).value).toBe("*x*");
  });
});

describe("heading", () => {
  it("prefixes the caret's line with ## and removes it again", () => {
    const s = heading(at("one\ntw|o\nthree"));
    expect(s.value).toBe("one\n## two\nthree");
    expect(s.selStart).toBe(9);
    expect(heading(s).value).toBe("one\ntwo\nthree");
  });
});

describe("bulletList", () => {
  it("prefixes each selected line with - ", () => {
    expect(bulletList(at("[a\nb]")).value).toBe("- a\n- b");
  });
});

describe("checklist", () => {
  it("prefixes each selected line with - [ ] ", () => {
    const s = checklist(at("x\n[milk\neggs]\ny"));
    expect(s.value).toBe("x\n- [ ] milk\n- [ ] eggs\ny");
    expect(selected(s)).toBe("- [ ] milk\n- [ ] eggs");
  });

  it("turns bullets into checklist items instead of stacking prefixes", () => {
    expect(checklist(at("[- milk\n- eggs]")).value).toBe("- [ ] milk\n- [ ] eggs");
  });

  it("removes the prefix when every selected line already has it", () => {
    const value = "- [ ] milk\n- [x] eggs"; // brackets of its own: no at() template
    expect(checklist({ value, selStart: 0, selEnd: value.length }).value).toBe("milk\neggs");
  });

  it("works on the caret's line with nothing selected", () => {
    const s = checklist(at("bu|y"));
    expect(s.value).toBe("- [ ] buy");
    expect(s.selStart).toBe(8);
  });
});

describe("code", () => {
  it("wraps a single-line selection in backticks", () => {
    expect(code(at("run [npm ci] first")).value).toBe("run `npm ci` first");
  });

  it("inserts a pair of backticks with the caret inside", () => {
    const s = code(at("a|"));
    expect(s.value).toBe("a``");
    expect(s.selStart).toBe(2);
  });

  it("fences a multi-line selection with a blank line before and after", () => {
    const s = code(at("intro\n[let a = 1\nlet b = 2]\noutro"));
    expect(s.value).toBe("intro\n\n```\nlet a = 1\nlet b = 2\n```\n\noutro");
    expect(selected(s)).toBe("let a = 1\nlet b = 2");
  });

  it("extends a partial multi-line selection to whole lines", () => {
    expect(code(at("le[t a\nlet] b")).value).toBe("```\nlet a\nlet b\n```");
  });
});

describe("link", () => {
  it("makes the selection the link text and selects the URL", () => {
    const s = link(at("see [docs] here"));
    expect(s.value).toBe("see [docs](https://) here");
    expect(selected(s)).toBe("https://");
  });

  it("makes a selected URL the target and puts the caret in the text", () => {
    const s = link(at("[https://example.com]"));
    expect(s.value).toBe("[](https://example.com)");
    expect(s.selStart).toBe(1);
  });

  it("selects placeholder text when nothing is selected", () => {
    const s = link(at("|"));
    expect(s.value).toBe("[link](https://)");
    expect(selected(s)).toBe("link");
  });
});

describe("images", () => {
  it("insertImage puts ![name](att:id) at the caret", () => {
    const s = insertImage(at("a |b"), "shot", "01ARZ3NDEKTSV4RRFFQ69G5FAV");
    expect(s.value).toBe("a ![shot](att:01ARZ3NDEKTSV4RRFFQ69G5FAV)b");
    expect(s.selStart).toBe(s.value.length - 1);
  });

  it("imageMarkdown strips characters that would break the syntax", () => {
    expect(imageMarkdown("my [best]\nshot", "X")).toBe("![my best shot](att:X)");
    expect(imageMarkdown("  ", "X")).toBe("![image](att:X)");
  });

  it("replaceMarker swaps a placeholder and keeps the caret where the user is", () => {
    const marker = "![Uploading…](uploading:1)";
    let s = insertText(at("a |b"), marker);
    s = { ...s, value: s.value + " typed", selStart: s.value.length + 6, selEnd: s.value.length + 6 };
    const out = replaceMarker(s, marker, "![x](att:ID)");
    expect(out?.value).toBe("a ![x](att:ID)b typed");
    expect(out?.selStart).toBe(out!.value.length);
  });

  it("replaceMarker leaves a caret before the placeholder alone", () => {
    const marker = "![Uploading…](uploading:2)";
    const s = { value: `ab ${marker}`, selStart: 1, selEnd: 1 };
    expect(replaceMarker(s, marker, "")).toEqual({ value: "ab ", selStart: 1, selEnd: 1 });
  });

  it("replaceMarker returns null when the placeholder is gone", () => {
    expect(replaceMarker(at("x|"), "![Uploading…](uploading:3)", "y")).toBeNull();
  });
});
