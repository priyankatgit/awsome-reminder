// Several tasks typed into the capture box at once (black glossy theme
// design, R9): Shift+Enter stages the line in the review list, and a paste of
// several lines stages all but the last. This module holds the pure part.

/** A list marker at the start of a pasted line: "- ", "* ", "• ", "1. ", "2) ", "[ ] ", "- [x] ". */
const MARKER = /^\s*(?:[-*•+]\s+)?(?:\[[ xX]\]\s+)?(?:\d{1,3}[.)]\s+)?/;

/**
 * The tasks in a pasted text: one per non-empty line, trimmed, with list
 * markers (bullets, numbers, checkboxes) dropped. A single line gives one
 * entry (the caller pastes it normally).
 */
export function splitPasted(text: string): string[] {
  return text
    .split(/\r\n|\r|\n/)
    .map((l) => l.replace(MARKER, "").trim())
    .filter((l) => l !== "");
}

/**
 * Where a multi-line paste lands, given the input's value and selection: the
 * text before the selection joins the first pasted line, the text after it
 * joins the last. Returns the lines for the review list and the input's new
 * value with its caret (after the pasted part).
 */
export function placePaste(
  value: string,
  start: number,
  end: number,
  lines: string[],
): { rows: string[]; input: string; caret: number } {
  const before = value.slice(0, start);
  const after = value.slice(end);
  const all = [...lines];
  all[0] = `${before}${all[0]}`;
  const last = all.pop() ?? "";
  const input = `${last}${after}`;
  return {
    rows: all.map((l) => l.trim()).filter((l) => l !== ""),
    input,
    caret: last.length,
  };
}
