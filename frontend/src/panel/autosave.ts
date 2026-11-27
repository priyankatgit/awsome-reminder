// Debounced autosave for one text field of the detail panel. It holds at most
// one unsaved value: a newer value for the same item replaces it, and a value
// for another item first sends the pending one to its own item (a panel switch
// never lands an edit on the wrong item). Saves run one at a time, in order.
// The status stays "error" while any item's latest save has failed, even if
// another item saves fine after it.

export type SaveStatus = "idle" | "saving" | "saved" | "error";

export interface Autosaver<T> {
  /** Records the latest value for itemId; it is saved after delayMs idle. */
  change(itemId: string, value: T): void;
  /** Saves any pending value now; resolves once every save has settled. Never rejects. */
  flush(): Promise<void>;
  status(): SaveStatus;
  /** Calls cb on every status change; returns the unsubscribe function. */
  onStatus(cb: (s: SaveStatus) => void): () => void;
}

export function createAutosaver<T>(
  save: (itemId: string, value: T) => Promise<void>,
  delayMs = 600,
): Autosaver<T> {
  let pending: { itemId: string; value: T } | null = null;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let chain: Promise<void> = Promise.resolve();
  let running = 0;
  const failedItems = new Set<string>();
  let current: SaveStatus = "idle";
  const subs = new Set<(s: SaveStatus) => void>();

  function setStatus(s: SaveStatus): void {
    if (s === current) return;
    current = s;
    subs.forEach((cb) => cb(s));
  }

  /** Sends the pending value (if any) after the saves already queued. */
  function send(): Promise<void> {
    clearTimeout(timer);
    timer = undefined;
    const p = pending;
    pending = null;
    if (!p) return chain;
    running++;
    setStatus("saving");
    chain = chain
      .then(() => save(p.itemId, p.value))
      .then(
        () => {
          running--;
          failedItems.delete(p.itemId);
          if (running === 0) setStatus(failedItems.size ? "error" : "saved");
        },
        () => {
          running--;
          failedItems.add(p.itemId);
          setStatus("error");
        },
      );
    return chain;
  }

  return {
    change(itemId, value) {
      if (pending && pending.itemId !== itemId) void send();
      pending = { itemId, value };
      clearTimeout(timer);
      timer = setTimeout(() => void send(), delayMs);
    },
    flush: send,
    status: () => current,
    onStatus(cb) {
      subs.add(cb);
      return () => subs.delete(cb);
    },
  };
}
