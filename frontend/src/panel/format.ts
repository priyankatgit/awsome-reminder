// Pure Markdown helpers for the description toolbar. Each takes the textarea's
// text and selection and returns the new text and selection; the editor
// applies the result. Nothing here touches the DOM.

export interface TextState {
  value: string;
  selStart: number;
  selEnd: number;
}

const URL_LIKE = /^(https?:\/\/|mailto:)\S+$/i;

/** Replaces the selection with text; the caret lands after it. */
export function insertText(s: TextState, text: string): TextState {
  const value = s.value.slice(0, s.selStart) + text + s.value.slice(s.selEnd);
  const caret = s.selStart + text.length;
  return { value, selStart: caret, selEnd: caret };
}

/** Wraps the selection in mark; with nothing selected the caret sits between the marks. */
function wrap(s: TextState, open: string, close = open): TextState {
  const sel = s.value.slice(s.selStart, s.selEnd);
  const value = s.value.slice(0, s.selStart) + open + sel + close + s.value.slice(s.selEnd);
  const start = s.selStart + open.length;
  return { value, selStart: start, selEnd: start + sel.length };
}

export const bold = (s: TextState) => wrap(s, "**");
export const italic = (s: TextState) => wrap(s, "*");

/** Start of the line holding pos, and end of the line holding end. */
function lineSpan(value: string, pos: number, end: number): [number, number] {
  const from = value.lastIndexOf("\n", pos - 1) + 1;
  // A selection that ends right after a newline doesn't take in the next line.
  const last = end > pos && value[end - 1] === "\n" ? end - 1 : end;
  const nl = value.indexOf("\n", last);
  return [from, nl === -1 ? value.length : nl];
}

/**
 * Adds prefix to every line the selection touches, or removes it when every
 * line already has it. strip matches a prefix to replace (a bullet becoming a
 * checklist item); has matches the prefix itself.
 */
function prefixLines(s: TextState, prefix: string, has: RegExp, strip?: RegExp): TextState {
  const [from, to] = lineSpan(s.value, s.selStart, s.selEnd);
  const lines = s.value.slice(from, to).split("\n");
  const off = lines.every((l) => has.test(l));
  const out = lines.map((l) => (off ? l.replace(has, "") : prefix + (strip ? l.replace(strip, "") : l))).join("\n");
  const value = s.value.slice(0, from) + out + s.value.slice(to);
  if (s.selStart !== s.selEnd) return { value, selStart: from, selEnd: from + out.length };
  // A caret moves with its line's change, but never before the line start.
  const caret = Math.max(from, s.selStart + (out.length - (to - from)));
  return { value, selStart: caret, selEnd: caret };
}

export const heading = (s: TextState) => prefixLines(s, "## ", /^## /);
export const bulletList = (s: TextState) => prefixLines(s, "- ", /^- (?!\[[ xX]\] )/, /^[-*+] /);
export const checklist = (s: TextState) => prefixLines(s, "- [ ] ", /^[-*+] \[[ xX]\] /, /^[-*+] /);

/**
 * Inline code for a single line, a fenced block (on whole lines, set apart by
 * blank lines) for several.
 */
export function code(s: TextState): TextState {
  const sel = s.value.slice(s.selStart, s.selEnd);
  if (!sel.includes("\n")) return wrap(s, "`");
  const [from, to] = lineSpan(s.value, s.selStart, s.selEnd);
  const body = s.value.slice(from, to);
  let before = s.value.slice(0, from);
  let after = s.value.slice(to);
  if (before !== "" && !before.endsWith("\n\n")) before += "\n";
  if (after !== "" && !after.startsWith("\n\n")) after = "\n" + after;
  const start = before.length + 4; // "```\n"
  return { value: `${before}\`\`\`\n${body}\n\`\`\`${after}`, selStart: start, selEnd: start + body.length };
}

/**
 * A link around the selection with the URL selected for typing; a selected
 * URL becomes the target instead, with the caret in the empty text.
 */
export function link(s: TextState): TextState {
  const sel = s.value.slice(s.selStart, s.selEnd);
  const before = s.value.slice(0, s.selStart);
  const after = s.value.slice(s.selEnd);
  if (URL_LIKE.test(sel)) {
    const caret = s.selStart + 1;
    return { value: `${before}[](${sel})${after}`, selStart: caret, selEnd: caret };
  }
  if (sel === "") {
    const start = s.selStart + 1;
    return { value: `${before}[link](https://)${after}`, selStart: start, selEnd: start + 4 };
  }
  const urlStart = s.selStart + sel.length + 3; // "[" + sel + "]("
  return { value: `${before}[${sel}](https://)${after}`, selStart: urlStart, selEnd: urlStart + 8 };
}

/** The Markdown for a stored image; name loses anything that would break the syntax. */
export function imageMarkdown(name: string, id: string): string {
  const alt = name.replace(/[[\]\\]/g, "").replace(/\s+/g, " ").trim() || "image";
  return `![${alt}](att:${id})`;
}

export function insertImage(s: TextState, name: string, id: string): TextState {
  return insertText(s, imageMarkdown(name, id));
}

/**
 * Replaces the first occurrence of marker (an upload placeholder) wherever
 * the user's edits have moved it; a selection after it shifts with it. null
 * when the marker is gone (the user deleted it).
 */
export function replaceMarker(s: TextState, marker: string, replacement: string): TextState | null {
  const at = s.value.indexOf(marker);
  if (at === -1) return null;
  const end = at + marker.length;
  const shift = (pos: number) => (pos >= end ? pos + replacement.length - marker.length : Math.min(pos, at));
  return {
    value: s.value.slice(0, at) + replacement + s.value.slice(end),
    selStart: shift(s.selStart),
    selEnd: shift(s.selEnd),
  };
}
