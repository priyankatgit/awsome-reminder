// Priority helpers shared by the capture box and the detail panel. The typed
// token stays the single source of truth: these helpers only rewrite the text,
// and the Go parser (internal/parser/priority.go) decides what it means.

export type Priority = "" | "low" | "medium" | "high";

/** Whole unquoted tokens the parser reads as a priority (case-insensitive). */
const TOKENS = new Set(["!high", "!h", "!!!", "!medium", "!med", "!m", "!low", "!l"]);

/** The form Render emits, so a rewritten line round-trips unchanged. */
const EMIT: Record<Exclude<Priority, "">, string> = {
  high: "!high",
  medium: "!med",
  low: "!low",
};

const LABELS: Record<Priority, string> = {
  high: "High",
  medium: "Medium",
  low: "Low",
  "": "None",
};

/** "High", "Medium", "Low", or "None" for no priority. */
export function priorityLabel(p: Priority): string {
  return LABELS[p];
}

const isSpace = (c: string) => c === " " || c === "\t" || c === "\n" || c === "\r" || c === "\v" || c === "\f";

interface Span {
  start: number;
  end: number;
  quoted: boolean;
}

/**
 * Token spans exactly as the parser's tokenize sees them: whitespace-separated,
 * except that a double quote at a token boundary runs to the next double quote
 * as one literal token. An unclosed quote is ordinary text.
 */
function spans(s: string): Span[] {
  const out: Span[] = [];
  let i = 0;
  while (i < s.length) {
    if (isSpace(s[i])) {
      i++;
      continue;
    }
    if (s[i] === '"') {
      const close = s.indexOf('"', i + 1);
      if (close >= 0) {
        out.push({ start: i, end: close + 1, quoted: true });
        i = close + 1;
        continue;
      }
    }
    let j = i;
    while (j < s.length && !isSpace(s[j])) j++;
    out.push({ start: i, end: j, quoted: false });
    i = j;
  }
  return out;
}

/**
 * Sets the priority token in a capture line. The first standalone token is
 * replaced in place and any others are dropped (the parser would ignore them);
 * with none present the word form is appended. "" removes every token, with
 * the whitespace in front of it. Quoted text is never touched.
 */
export function setPriorityToken(text: string, p: Priority): string {
  const hits = spans(text).filter((t) => !t.quoted && TOKENS.has(text.slice(t.start, t.end).toLowerCase()));
  if (hits.length === 0) {
    if (p === "") return text;
    const base = text.trimEnd();
    return base === "" ? EMIT[p] : `${base} ${EMIT[p]}`;
  }
  let out = text;
  // Edit from the end so earlier offsets stay valid.
  for (let k = hits.length - 1; k >= 0; k--) {
    const { start, end } = hits[k];
    if (k === 0 && p !== "") {
      out = out.slice(0, start) + EMIT[p] + out.slice(end);
      continue;
    }
    let from = start;
    let to = end;
    while (from > 0 && isSpace(out[from - 1])) from--;
    if (from === 0) while (to < out.length && isSpace(out[to])) to++; // leading token: drop the space after it
    out = out.slice(0, from) + out.slice(to);
  }
  return out;
}
