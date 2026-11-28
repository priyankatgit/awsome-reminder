// Review list for the capture box (Phase 3): tasks dictated one after another
// wait here until Enter (or a spoken "save all") saves them in order.
import { api, errorText, type Item, type Preview } from "./api";
import { el, tagChip } from "./dom";
import { icons } from "./icons";

/**
 * Saves texts in order and stops at the first failure, so the rows after it
 * stay unsaved. `failedAt` is the index in `texts` of the row that failed.
 */
export async function saveSequential(
  texts: string[],
  capture: (t: string) => Promise<Item>,
): Promise<{ saved: Item[]; failedAt: number | null; error: string }> {
  const saved: Item[] = [];
  for (let i = 0; i < texts.length; i++) {
    try {
      saved.push(await capture(texts[i]));
    } catch (err) {
      return { saved, failedAt: i, error: errorText(err) };
    }
  }
  return { saved, failedAt: null, error: "" };
}

interface Row {
  text: string;
  preview: Preview | null;
  error: string;
}

export interface ReviewListOptions {
  /** Enter on a row: move its text into the input. */
  onEdit?: (i: number) => void;
  /** Esc on a row: back to the input. */
  onBack?: () => void;
}

export class ReviewList {
  private rows: Row[] = [];

  constructor(
    private container: HTMLElement,
    private onChange: () => void,
    private opts: ReviewListOptions = {},
  ) {
    container.setAttribute("role", "list");
    container.setAttribute("aria-label", "Tasks to save");
    container.classList.add("rv-list");
    container.addEventListener("keydown", (e) => this.onKey(e));
    this.render();
  }

  items(): string[] {
    return this.rows.map((r) => r.text);
  }

  add(text: string): void {
    const row: Row = { text, preview: null, error: "" };
    this.rows.push(row);
    this.changed();
    void api
      .preview(text)
      .then((p) => {
        row.preview = p;
        if (this.rows.includes(row)) this.render();
      })
      .catch(() => {});
  }

  removeAt(i: number): void {
    if (i < 0 || i >= this.rows.length) return;
    this.rows.splice(i, 1);
    this.changed();
  }

  popLast(): string | undefined {
    const r = this.rows.pop();
    if (r) this.changed();
    return r?.text;
  }

  takeAt(i: number): string {
    const r = this.rows[i];
    if (!r) return "";
    this.removeAt(i);
    return r.text;
  }

  /** Replaces the whole list (Undo, or leftovers failing to save). */
  restore(items: string[]): void {
    this.rows = [];
    this.container.replaceChildren();
    for (const t of items) {
      const row: Row = { text: t, preview: null, error: "" };
      this.rows.push(row);
      void api
        .preview(t)
        .then((p) => {
          row.preview = p;
          if (this.rows.includes(row)) this.render();
        })
        .catch(() => {});
    }
    this.changed();
  }

  clear(): void {
    if (this.rows.length === 0) return;
    this.rows = [];
    this.changed();
  }

  setError(i: number, msg: string): void {
    if (!this.rows[i]) return;
    this.rows[i].error = msg;
    this.render();
  }

  focusLast(): void {
    this.rowEls().at(-1)?.focus();
  }

  private rowEls(): HTMLElement[] {
    return [...this.container.querySelectorAll<HTMLElement>(".rv-row")];
  }

  private changed(): void {
    this.render();
    this.onChange();
  }

  private render(): void {
    const had = this.container.contains(document.activeElement) ? this.rowEls().indexOf(document.activeElement as HTMLElement) : -1;
    this.container.replaceChildren(...this.rows.map((r, i) => this.renderRow(r, i)));
    if (had >= 0) this.rowEls()[Math.min(had, this.rows.length - 1)]?.focus();
  }

  private renderRow(r: Row, i: number): HTMLElement {
    const row = el("div", "rv-row");
    row.setAttribute("role", "listitem");
    row.tabIndex = -1;
    row.dataset.index = String(i);
    row.append(el("span", "rv-num", String(i + 1)));
    const kind = el("span", "rv-kind");
    kind.innerHTML = r.preview?.dueAt ? icons.bell : icons.todo;
    kind.setAttribute("aria-hidden", "true");
    row.append(kind);
    const text = el("span", "rv-text", r.text);
    text.title = r.text;
    row.append(text);
    const p = r.preview;
    if (p) {
      const meta = el("span", "rv-meta");
      if (p.priority) {
        const dot = el("span", "prio-dot rv-prio");
        dot.dataset.prio = p.priority;
        dot.title = `${p.priority} priority`;
        meta.append(dot);
      }
      if (p.dueAt) meta.append(el("span", "chip chip-remind", `Reminder · ${p.human}`));
      for (const t of p.tags ?? []) meta.append(tagChip(t));
      row.append(meta);
    }
    const rm = el("button", "icon-btn rv-remove");
    rm.type = "button";
    rm.setAttribute("aria-label", `Remove task ${i + 1}`);
    rm.innerHTML = icons.close;
    rm.addEventListener("click", () => {
      this.removeAt(i);
      this.opts.onBack?.();
    });
    row.append(rm);
    if (r.error) {
      row.classList.add("has-error");
      const err = el("span", "rv-error", r.error);
      err.setAttribute("role", "alert");
      row.append(err);
    }
    return row;
  }

  private onKey(e: KeyboardEvent): void {
    const target = (e.target as HTMLElement).closest<HTMLElement>(".rv-row");
    if (!target || e.isComposing) return;
    const i = Number(target.dataset.index);
    switch (e.key) {
      case "ArrowUp":
        e.preventDefault();
        this.rowEls()[Math.max(i - 1, 0)]?.focus();
        break;
      case "ArrowDown": {
        e.preventDefault();
        const rows = this.rowEls();
        if (i >= rows.length - 1) this.opts.onBack?.();
        else rows[i + 1].focus();
        break;
      }
      case "Delete":
        e.preventDefault();
        this.removeAt(i);
        if (this.rows.length === 0) this.opts.onBack?.();
        break;
      case "Enter":
        if ((e.target as HTMLElement).closest("button")) return; // the × button handles its own Enter
        e.preventDefault();
        this.opts.onEdit?.(i);
        break;
      case "Escape":
        e.preventDefault();
        e.stopPropagation(); // not the box's own Esc
        this.opts.onBack?.();
        break;
    }
  }
}
