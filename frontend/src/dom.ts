// Small DOM and styling helpers shared by the views.

const TAG_HUES = 8;

const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)");

/** Duration of a CSS time token in ms (0 under reduced motion). */
export function motionMs(token: string): number {
  if (reducedMotion.matches) return 0;
  const v = getComputedStyle(document.documentElement).getPropertyValue(token).trim();
  const n = parseFloat(v);
  if (Number.isNaN(n)) return 0;
  return v.endsWith("ms") ? n : n * 1000;
}

/** Stable tag colour slot: FNV-1a over the tag, mod 8. "inbox" is neutral. */
export function tagSlot(tag: string): string {
  if (tag === "inbox") return "inbox";
  let h = 0x811c9dc5;
  for (let i = 0; i < tag.length; i++) {
    h ^= tag.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return String(h % TAG_HUES);
}

export function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

export function chip(text: string, cls: string): HTMLElement {
  return el("span", `chip ${cls}`, text);
}

/** A hashed-colour tag chip ("#name"). */
export function tagChip(tag: string): HTMLElement {
  const c = chip(`#${tag}`, "chip-tag");
  c.dataset.hue = tagSlot(tag);
  return c;
}

/** Renders a hotkey combo ("Ctrl+Shift+Space") as keycaps. */
export function keycaps(combo: string): DocumentFragment {
  const f = document.createDocumentFragment();
  combo
    .split("+")
    .map((k) => k.trim())
    .filter((k) => k !== "")
    .forEach((k, i) => {
      if (i > 0) f.append(el("span", "keycap-plus", "+"));
      f.append(el("kbd", undefined, k));
    });
  return f;
}
