// A small accessible popup menu (role=menu): arrow keys, Home/End, first
// letter type-ahead, Enter/Space to run, Esc or Tab to close. Focus returns
// to the element given as returnFocus. One menu is open at a time.
import { el } from "./dom";
import { icons, type IconName } from "./icons";

export interface MenuItem {
  label: string;
  icon?: IconName;
  danger?: boolean;
  run: () => void;
}

export interface MenuOptions {
  /** Accessible name of the menu. */
  label: string;
  /** Focused again when the menu closes by keyboard or by running an item. */
  returnFocus: HTMLElement;
  /** Gets aria-expanded while the menu is open. */
  trigger?: HTMLElement;
  onClose?: () => void;
}

interface OpenMenu {
  node: HTMLElement;
  opts: MenuOptions;
  cleanup: () => void;
}

let current: OpenMenu | null = null;

export function isMenuOpen(): boolean {
  return current !== null;
}

/** Closes the open menu, if any; returnFocus moves focus back. */
export function closeMenu(returnFocus = true): void {
  const m = current;
  if (!m) return;
  current = null;
  m.cleanup();
  m.node.remove();
  m.opts.trigger?.setAttribute("aria-expanded", "false");
  if (returnFocus) m.opts.returnFocus.focus({ preventScroll: true });
  m.opts.onClose?.();
}

function cssPx(token: string): number {
  return parseFloat(getComputedStyle(document.documentElement).getPropertyValue(token)) || 0;
}

/** Places the menu under anchor, right-aligned; above it when it won't fit. */
function position(node: HTMLElement, anchor: DOMRect): void {
  const gap = cssPx("--space-1");
  const edge = cssPx("--space-2");
  const r = node.getBoundingClientRect();
  let left = anchor.right - r.width;
  left = Math.min(Math.max(left, edge), window.innerWidth - r.width - edge);
  let top = anchor.bottom + gap;
  if (top + r.height > window.innerHeight - edge) top = Math.max(anchor.top - gap - r.height, edge);
  node.style.left = `${Math.round(left)}px`;
  node.style.top = `${Math.round(top)}px`;
}

/** Opens a menu for anchor (an element, or a point such as a right-click). */
export function openMenu(anchor: HTMLElement | DOMRect, items: MenuItem[], opts: MenuOptions): void {
  closeMenu(false);
  if (items.length === 0) return;

  const node = el("div", "menu");
  node.setAttribute("role", "menu");
  node.setAttribute("aria-label", opts.label);
  const buttons = items.map((it) => {
    const b = el("button", it.danger ? "menu-item is-danger" : "menu-item");
    b.type = "button";
    b.tabIndex = -1;
    b.setAttribute("role", "menuitem");
    if (it.icon) b.innerHTML = icons[it.icon];
    b.append(el("span", undefined, it.label));
    b.addEventListener("click", () => {
      closeMenu(true);
      it.run();
    });
    node.append(b);
    return b;
  });

  const focusAt = (i: number) => buttons[(i + buttons.length) % buttons.length].focus();
  const index = () => buttons.indexOf(document.activeElement as HTMLButtonElement);

  node.addEventListener("keydown", (e) => {
    e.stopPropagation(); // the list's own shortcuts stay off while the menu is open
    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        focusAt(index() + 1);
        return;
      case "ArrowUp":
        e.preventDefault();
        focusAt(index() < 0 ? -1 : index() - 1);
        return;
      case "Home":
        e.preventDefault();
        focusAt(0);
        return;
      case "End":
        e.preventDefault();
        focusAt(-1);
        return;
      case "Escape":
      case "Tab":
        e.preventDefault();
        closeMenu(true);
        return;
      case "Enter":
      case " ":
        e.preventDefault();
        (document.activeElement as HTMLElement | null)?.click();
        return;
    }
    if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
      const k = e.key.toLowerCase();
      const start = index() + 1;
      for (let n = 0; n < buttons.length; n++) {
        const b = buttons[(start + n) % buttons.length];
        if (b.textContent?.trim().toLowerCase().startsWith(k)) {
          b.focus();
          break;
        }
      }
    }
  });

  const outside = (e: PointerEvent) => {
    if (!node.contains(e.target as Node)) closeMenu(false);
  };
  const dismiss = () => closeMenu(false);
  document.addEventListener("pointerdown", outside, true);
  window.addEventListener("blur", dismiss);
  window.addEventListener("resize", dismiss);
  document.addEventListener("scroll", dismiss, true);

  document.body.append(node);
  position(node, anchor instanceof HTMLElement ? anchor.getBoundingClientRect() : anchor);
  opts.trigger?.setAttribute("aria-expanded", "true");
  current = {
    node,
    opts,
    cleanup: () => {
      document.removeEventListener("pointerdown", outside, true);
      window.removeEventListener("blur", dismiss);
      window.removeEventListener("resize", dismiss);
      document.removeEventListener("scroll", dismiss, true);
    },
  };
  buttons[0].focus({ preventScroll: true });
}
