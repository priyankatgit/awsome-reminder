// Turns one transcribed piece of speech into capture-box operations.
export type Op =
  | { kind: "text"; text: string }
  | { kind: "boundary" }
  | { kind: "scratch" }
  | { kind: "save" }
  | { kind: "cancel" };

const BOUNDARY = ["new task", "create new task", "create a new task", "next task", "add another task"];
const SCRATCH = ["scratch that", "delete that"];
const SAVE = ["save all", "save them", "save them all", "save tasks", "thats all", "thats it"];
/** A wake box nobody engaged with yet saves only on these: chatter often ends "that's it". */
const STRICT_SAVE = ["save all", "save tasks", "save them all"];
const CANCEL = ["cancel", "cancel all", "never mind"];

// Curly quotes and the ellipsis character count as punctuation when matching.
const PUNCT = /[.,!?;:"'‘’“”…]/g;
const EDGE_PUNCT = /^[\s.,!?;:"']+|[\s.,!?;:"']+$/g;

const norm = (w: string): string => w.toLowerCase().replace(PUNCT, "");
const words = (s: string): string[] => s.split(/\s+/).filter(Boolean);
const phraseWords = (p: string): string[] => p.split(" ");
const BOUNDARY_PHRASES = BOUNDARY.map(phraseWords).sort((a, b) => b.length - a.length);

function matchesWhole(sentence: string, phrases: string[]): boolean {
  const n = words(sentence).map(norm).filter(Boolean).join(" ");
  return phrases.includes(n);
}

function isFiller(w: string): boolean {
  const n = norm(w);
  return n === "and" || n === "okay";
}

function textOp(ws: string[], trimStart: boolean, trimEnd: boolean): Op[] {
  let a = 0;
  let b = ws.length;
  while (trimStart && a < b && isFiller(ws[a])) a++;
  while (trimEnd && b > a && isFiller(ws[b - 1])) b--;
  const text = ws.slice(a, b).join(" ").replace(EDGE_PUNCT, "");
  return text ? [{ kind: "text", text }] : [];
}

function splitBoundaries(text: string): Op[] {
  const ws = words(text);
  const norms = ws.map(norm);
  const ops: Op[] = [];
  let start = 0;
  let afterBoundary = false;
  let i = 0;
  while (i < ws.length) {
    const phrase = BOUNDARY_PHRASES.find((p) => p.every((pw, k) => norms[i + k] === pw));
    if (phrase) {
      ops.push(...textOp(ws.slice(start, i), afterBoundary, true), { kind: "boundary" });
      i += phrase.length;
      start = i;
      afterBoundary = true;
    } else {
      i++;
    }
  }
  ops.push(...textOp(ws.slice(start), afterBoundary, false));
  return ops;
}

export interface ParseOptions {
  /** False in reply mode: one line of text, no boundaries, saves or cancels. */
  multi?: boolean;
  /** Only "save all", "save tasks" and "save them all" save (an unengaged wake box). */
  strictSave?: boolean;
}

export function parseSegment(text: string, opts: ParseOptions = {}): Op[] {
  const multi = opts.multi ?? true;
  const save = opts.strictSave ? STRICT_SAVE : SAVE;

  // The last sentence (or the whole segment) may be a command.
  const sentences = [...text.matchAll(/[^.?!]+[.?!]*/g)].filter((m) => norm(m[0]).trim() !== "");
  const last = sentences[sentences.length - 1];
  let command: Op | null = null;
  let body = text;
  if (last) {
    const s = last[0];
    if (matchesWhole(s, SCRATCH)) command = { kind: "scratch" };
    else if (multi && matchesWhole(s, save)) command = { kind: "save" };
    else if (multi && matchesWhole(s, CANCEL)) command = { kind: "cancel" };
    if (command) body = text.slice(0, last.index);
  }

  const ops: Op[] = multi ? splitBoundaries(body) : textOp(words(body), false, false);
  if (command) ops.push(command);
  return ops;
}
