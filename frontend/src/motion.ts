// Motion helpers on the Web Animations API: token lookups, FLIP glides and
// one-off effects. Every helper is a no-op under prefers-reduced-motion (or
// where the API is missing, as in jsdom), and nothing here loops: each
// animation is started by a user action or a data change and ends on its own.
import { motionMs } from "./dom";

const reducedMQ = window.matchMedia("(prefers-reduced-motion: reduce)");

/** Whether the user asked for reduced motion. */
export function reducedMotion(): boolean {
  return reducedMQ.matches;
}

function token(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

/** A unitless or px/deg token as a number (0 if missing). */
export function tokenNumber(name: string): number {
  const n = parseFloat(token(name));
  return Number.isNaN(n) ? 0 : n;
}

/**
 * An easing token for element.animate(). A linear() spring that this engine
 * can't parse (older WebKit) would throw, so it falls back to --ease-enter.
 */
export function easing(name: string): string {
  const v = token(name);
  const ok = v !== "" && typeof CSS !== "undefined" && typeof CSS.supports === "function" && CSS.supports("transition-timing-function", v);
  return ok ? v : token("--ease-enter") || "ease-out";
}

/** Whether animations can run on el at all (API present, motion allowed). */
export function canAnimate(el: Element): boolean {
  return !reducedMotion() && typeof (el as HTMLElement).animate === "function";
}

// ---------- FLIP ----------

function offscreen(r: DOMRect): boolean {
  return r.bottom < 0 || r.top > window.innerHeight;
}

const glides = new WeakMap<Element, Animation>();

/**
 * Visual rects of els, in-flight glides included, so a glide interrupted by
 * the next one continues from where the element is on screen.
 */
export function measure(els: Iterable<Element>): Map<Element, DOMRect> {
  const out = new Map<Element, DOMRect>();
  for (const e of els) out.set(e, e.getBoundingClientRect());
  return out;
}

/**
 * Stops el's running glide (it snaps to its layout position). Call it on
 * every element before measuring where layout puts them.
 */
export function stopGlide(el: Element): void {
  glides.get(el)?.cancel();
  glides.delete(el);
}

/**
 * Glides el from the rect it had (first) to where layout now puts it: a
 * translate that starts at the difference and springs to none. Moves under
 * half a pixel, and moves wholly outside the viewport, are skipped.
 */
export function glide(
  el: HTMLElement,
  first: DOMRect,
  last?: DOMRect,
  opts: { duration?: number; easing?: string } = {},
): void {
  if (!last) {
    stopGlide(el);
    last = el.getBoundingClientRect();
  }
  if (!canAnimate(el)) return;
  const dx = first.left - last.left;
  const dy = first.top - last.top;
  if (Math.abs(dx) < 0.5 && Math.abs(dy) < 0.5) return;
  if (offscreen(first) && offscreen(last)) return;
  stopGlide(el);
  const duration = opts.duration ?? motionMs("--dur-glide");
  if (duration <= 0) return;
  const a = el.animate([{ transform: `translate(${dx}px, ${dy}px)` }, { transform: "none" }], {
    duration,
    easing: opts.easing ?? easing("--ease-spring"),
  });
  glides.set(el, a);
  const clear = () => glides.get(el) === a && glides.delete(el);
  a.addEventListener("finish", clear);
  a.addEventListener("cancel", clear);
}

/** Fades a freshly inserted element in, with a small drop from offsetToken. */
export function enter(el: HTMLElement, offsetToken: string, rect?: DOMRect): void {
  if (!canAnimate(el) || offscreen(rect ?? el.getBoundingClientRect())) return;
  const y = tokenNumber(offsetToken);
  el.animate([{ opacity: 0, transform: `translateY(${y}px)` }, { opacity: 1, transform: "none" }], {
    duration: motionMs("--dur-enter"),
    easing: easing("--ease-spring"),
  });
}

/**
 * A fading copy of a removed element, left where it was (inside host, which
 * must be positioned) and dropped from the DOM when the fade ends.
 */
export function ghost(old: HTMLElement, rect: DOMRect, host: HTMLElement, hostRect?: DOMRect): void {
  if (!canAnimate(host) || offscreen(rect) || rect.height === 0) return;
  const h = hostRect ?? host.getBoundingClientRect(); // callers with many ghosts pass it once
  const g = old.cloneNode(true) as HTMLElement;
  g.removeAttribute("id");
  g.querySelectorAll("[id]").forEach((n) => n.removeAttribute("id"));
  g.classList.add("is-ghost");
  g.setAttribute("aria-hidden", "true");
  g.inert = true;
  Object.assign(g.style, {
    position: "absolute",
    left: `${rect.left - h.left + host.scrollLeft}px`,
    top: `${rect.top - h.top + host.scrollTop}px`,
    width: `${rect.width}px`,
    height: `${rect.height}px`,
    margin: "0",
  });
  host.append(g);
  const a = g.animate([{ opacity: 1 }, { opacity: 0 }], {
    fill: "forwards", // stays invisible even if the finish event comes late (hidden window)
    duration: motionMs("--dur-exit"),
    easing: token("--ease-exit") || "ease-in",
  });
  const drop = () => g.remove();
  a.addEventListener("finish", drop);
  a.addEventListener("cancel", drop);
}

// ---------- one-off effects ----------

/** Pops el once with the bouncy spring, from scaleToken up to its size. */
export function pop(el: Element, scaleToken: string, durationToken: string): void {
  if (!canAnimate(el)) return;
  const s = tokenNumber(scaleToken) || 1;
  el.animate([{ transform: `scale(${s})` }, { transform: "none" }], {
    duration: motionMs(durationToken),
    easing: easing("--ease-spring-pop"),
  });
}

/**
 * The bell rings once: a damped rotate wiggle after its pop. Rotates about
 * the top, where a bell hangs.
 */
export function ring(el: Element): void {
  if (!canAnimate(el)) return;
  const a = tokenNumber("--ring-angle");
  const turn = (k: number) => ({ transform: `rotate(${a * k}deg)` });
  el.animate([turn(0), turn(1), turn(-0.75), turn(0.45), turn(-0.2), turn(0)], {
    duration: motionMs("--dur-ring"),
    delay: motionMs("--dur-ring-delay"),
    easing: easing("--ease-ring"),
  });
}

/**
 * "Today is clear": a short burst of dots from (x, y) in viewport pixels.
 * Transform and opacity only; the layer leaves the DOM when the last dot
 * has faded.
 */
export function celebrate(x: number, y: number, host: HTMLElement = document.body): void {
  if (!canAnimate(host)) return;
  const count = Math.round(tokenNumber("--confetti-count"));
  const dist = tokenNumber("--confetti-distance");
  const spread = tokenNumber("--confetti-spread");
  const fall = tokenNumber("--confetti-fall");
  const small = tokenNumber("--confetti-start-scale");
  const duration = motionMs("--dur-confetti");
  if (count <= 0 || duration <= 0) return;
  const layer = document.createElement("div");
  layer.className = "confetti";
  layer.setAttribute("aria-hidden", "true");
  layer.style.left = `${x}px`;
  layer.style.top = `${y}px`;
  const ease = token("--ease-burst") || "ease-out";
  const runs: Promise<unknown>[] = [];
  for (let i = 0; i < count; i++) {
    const dot = document.createElement("i");
    layer.append(dot);
    const angle = ((i + Math.random() * spread) / count) * 2 * Math.PI;
    const r = dist * (1 + Math.random() * spread);
    const tx = Math.cos(angle) * r;
    const ty = Math.sin(angle) * r;
    const a = dot.animate(
      [
        { transform: `scale(${small})`, opacity: 1 },
        { transform: `translate(${tx}px, ${ty}px)`, opacity: 1, offset: 0.55 },
        { transform: `translate(${tx}px, ${ty + fall}px) scale(${small})`, opacity: 0 },
      ],
      { duration, easing: ease },
    );
    runs.push(a.finished);
  }
  host.append(layer);
  void Promise.allSettled(runs).then(() => layer.remove());
}
