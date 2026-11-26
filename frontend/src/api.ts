// Thin wrapper over the generated Wails bindings and events, so views never
// import generated paths directly.
import { Events, System } from "@wailsio/runtime";
import "../bindings/events/index.js"; // registers typed event data
import { Service } from "../bindings/awesome-reminder/internal/app/index.js";
import type { Preview } from "../bindings/awesome-reminder/internal/core/models.js";
import type { Item } from "../bindings/awesome-reminder/internal/model/models.js";
import type { Status } from "../bindings/awesome-reminder/internal/app/models.js";

export type { Preview, Item, Status };

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
};

/** Event names emitted by Go (internal/app/events.go). */
export const events = {
  itemsChanged: "items:changed",
  alertsShow: "alerts:show",
  listFocus: "list:focus",
  captureOpen: "capture:open",
} as const;

/** Subscribes to a Go event; returns the unsubscribe function. */
export function on<T = unknown>(name: string, fn: (data: T) => void): () => void {
  return Events.On(name, (ev) => fn(ev.data as T));
}

/** Whether the OS draws a translucent backdrop behind our floating windows. */
export function hasNativeBackdrop(): boolean {
  return System.IsWindows() || System.IsMac();
}

/** Message text of a rejected binding call. */
export function errorText(err: unknown): string {
  if (err instanceof Error) return err.message;
  if (typeof err === "object" && err !== null && "message" in err) return String((err as { message: unknown }).message);
  return String(err);
}
