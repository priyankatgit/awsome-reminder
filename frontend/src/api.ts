// Thin wrapper over the generated Wails bindings and events, so views never
// import generated paths directly.
import { Dialogs, Events, System, Window } from "@wailsio/runtime";
import "../bindings/events/index.js"; // registers typed event data
import { Service } from "../bindings/awesome-reminder/internal/app/index.js";
import type { Preview, TagGroup, View } from "../bindings/awesome-reminder/internal/core/models.js";
import type { Item } from "../bindings/awesome-reminder/internal/model/models.js";
import type { AlertCard as GenAlertCard, Status } from "../bindings/awesome-reminder/internal/app/models.js";

export type { Preview, Item, Status, TagGroup, View };

/** One card of the alert window (internal/app/alerts.go); tags never null. */
export type AlertCard = Omit<GenAlertCard, "tags"> & { tags: string[] };

/** Snooze presets understood by Service.Snooze. */
export type SnoozePreset = "10m" | "1h" | "tomorrow";

/** Settings as sent by Go (lead times are Go duration strings). */
export interface Settings {
  hotkey: string;
  leadTimes: string[];
  defaultTime: string; // "HH:MM"
  nextWeekDay: string;
  nextWeekTime: string;
  exportPath: string;
  autostart: boolean;
}

export const api = {
  preview: (input: string): Promise<Preview> => Service.Preview(input),
  withDue: (input: string, due: Date): Promise<string> => Service.WithDue(input, due.toISOString()),
  capture: (input: string): Promise<Item> => Service.Capture(input),
  getSettings: (): Promise<Settings> => Service.GetSettings() as Promise<Settings>,
  status: (): Promise<Status> => Service.Status(),
  hideCapture: (): Promise<void> => Service.HideCapture(),
  resizeCapture: (height: number): Promise<void> => Service.ResizeCapture(height),
  view: (): Promise<View> => Service.View(),
  done: (id: string): Promise<void> => Service.Done(id),
  reopen: (id: string): Promise<void> => Service.Reopen(id),
  snooze: (id: string, preset: SnoozePreset): Promise<void> => Service.Snooze(id, preset),
  deleteItem: (id: string): Promise<void> => Service.Delete(id),
  editText: (id: string): Promise<string> => Service.EditText(id),
  update: (id: string, input: string): Promise<Item> => Service.Update(id, input),
  stopRepeating: (id: string): Promise<void> => Service.StopRepeating(id),
  takePendingFocus: (): Promise<string> => Service.TakePendingFocus(),
  openSettings: (): Promise<void> => Service.OpenSettings(),
  openList: (focusItemId: string): Promise<void> => Service.OpenList(focusItemId),
  saveSettings: (s: Settings): Promise<void> => Service.SaveSettings(s as never),
  setHotkeyRecording: (on: boolean): Promise<void> => Service.SetHotkeyRecording(on),
  pendingAlerts: async (): Promise<AlertCard[]> => ((await Service.PendingAlerts()) ?? []) as AlertCard[],
  dismissAlert: (key: string): Promise<void> => Service.DismissAlert(key),
  dismissAlerts: (): Promise<void> => Service.DismissAlerts(),
  resizeAlerts: (height: number): Promise<void> => Service.ResizeAlerts(height),
  /** Save-file dialog for the Markdown export; "" when cancelled. */
  chooseExportPath: (current: string): Promise<string> => {
    const cut = Math.max(current.lastIndexOf("/"), current.lastIndexOf("\\"));
    return Dialogs.SaveFile({
      Title: "Export to Markdown",
      Filename: current.slice(cut + 1) || "todo.md",
      Directory: cut > 0 ? current.slice(0, cut) : "",
      CanCreateDirectories: true,
      Filters: [{ DisplayName: "Markdown (*.md)", Pattern: "*.md" }],
    });
  },
  /** Closes the window this page runs in. */
  closeWindow: (): Promise<void> => Window.Close(),
};

/** Event names emitted by Go (internal/app/events.go). */
export const events = {
  itemsChanged: "items:changed",
  alertsShow: "alerts:show",
  listFocus: "list:focus",
  captureOpen: "capture:open",
} as const;

const local = new Map<string, Set<(data: unknown) => void>>();

/** Subscribes to a Go event; returns the unsubscribe function. */
export function on<T = unknown>(name: string, fn: (data: T) => void): () => void {
  const cb = fn as (data: unknown) => void;
  if (!local.has(name)) local.set(name, new Set());
  local.get(name)!.add(cb);
  const off = Events.On(name, (ev) => fn(ev.data as T));
  return () => {
    local.get(name)?.delete(cb);
    off();
  };
}

/** Delivers an event to this page's own listeners only (dev preview data). */
export function emitLocal(name: string, data?: unknown): void {
  local.get(name)?.forEach((fn) => fn(data));
}

/** Whether the OS draws a translucent backdrop behind our floating windows. */
export function hasNativeBackdrop(): boolean {
  return System.IsWindows() || System.IsMac();
}

/** Whether we run on macOS (the Cmd modifier is offered only there). */
export function isMac(): boolean {
  return System.IsMac();
}

/** Message text of a rejected binding call. */
export function errorText(err: unknown): string {
  if (err instanceof Error) return err.message;
  if (typeof err === "object" && err !== null && "message" in err) return String((err as { message: unknown }).message);
  return String(err);
}
