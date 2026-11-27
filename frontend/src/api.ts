// Thin wrapper over the generated Wails bindings and events, so views never
// import generated paths directly.
import { Dialogs, Events, System, Window } from "@wailsio/runtime";
import "../bindings/events/index.js"; // registers typed event data
import { Service, VoiceService } from "../bindings/awesome-reminder/internal/app/index.js";
import type { Status as VoiceStatus } from "../bindings/awesome-reminder/internal/voice/models.js";
import type { Detail, PersonGroup, Preview, TagGroup, View } from "../bindings/awesome-reminder/internal/core/models.js";
import type { Attachment, Item } from "../bindings/awesome-reminder/internal/model/models.js";
import type { AlertCard as GenAlertCard, Status, SyncInfo } from "../bindings/awesome-reminder/internal/app/models.js";
import type { Status as GenSyncStatus } from "../bindings/awesome-reminder/internal/cloudsync/models.js";
import type { Meta as ReplyMeta } from "../bindings/awesome-reminder/internal/reply/models.js";

export type { Attachment, Detail, PersonGroup, Preview, Item, ReplyMeta, Status, SyncInfo, TagGroup, View, VoiceStatus };

/** A binding call that can be cancelled (Esc while transcribing). */
export type Cancellable<T> = Promise<T> & { cancel(): void };

/** Base64 of 16-bit PCM in little-endian byte order (every platform we run on). */
function pcmBase64(pcm: Int16Array): string {
  const bytes = new Uint8Array(pcm.buffer, pcm.byteOffset, pcm.byteLength);
  let bin = "";
  for (let i = 0; i < bytes.length; i += 0x8000) {
    bin += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  }
  return btoa(bin);
}

/** Alert card kinds (internal/scheduler Kind*): "" a normal alert, "nudge" an owed reply, "sweep" the end-of-day sweep. */
export type AlertKind = "" | "nudge" | "sweep";

/**
 * One card of the alert window (internal/app/alerts.go); tags and people
 * never null. A sweep card has itemId "" and lists the people replies are
 * owed to.
 */
export type AlertCard = Omit<GenAlertCard, "tags" | "people" | "kind"> & { tags: string[]; people: string[]; kind: AlertKind };

/** Machine sync state (internal/cloudsync State). */
export type SyncState = "off" | "syncing" | "synced" | "offline" | "error";

/** Sync status (internal/cloudsync Status); warnings never null. */
export type SyncStatus = Omit<GenSyncStatus, "state" | "warnings"> & { state: SyncState; warnings: string[] };

function syncStatus(s: GenSyncStatus): SyncStatus {
  return { ...s, state: s.state as string as SyncState, warnings: s.warnings ?? [] };
}

/** Base64 of a blob's bytes (a Go []byte argument travels as base64). */
async function blobBase64(b: Blob): Promise<string> {
  const url = await new Promise<string>((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(r.result as string);
    r.onerror = () => reject(r.error);
    r.readAsDataURL(b);
  });
  return url.slice(url.indexOf(",") + 1);
}

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
  showPendingCount: boolean;
  replyHotkey: string;
  /** Lowercase weekday names, Monday..Sunday order. */
  workDays: string[];
  endOfDay: string; // "HH:MM"
  endOfDaySweep: boolean;
  /** Stamp of the last shared-settings change (unix ms, device id). Set by Go; passed back unchanged. */
  sharedUpdatedAt?: number;
  sharedUpdatedBy?: string;
}

export const api = {
  preview: (input: string): Promise<Preview> => Service.Preview(input),
  withDue: (input: string, due: Date): Promise<string> => Service.WithDue(input, due.toISOString()),
  capture: (input: string): Promise<Item> => Service.Capture(input),
  /** Saves a reply owed: who it is owed to, where it came from, the quoted message. */
  captureReply: (input: string, meta: ReplyMeta): Promise<Item> => Service.CaptureReply(input, meta),
  /** Closes a reply without answering it. */
  dropReply: (id: string): Promise<void> => Service.DropReply(id),
  /** The sweep's Tomorrow: snoozes every open reply to the next working day at the default time. */
  snoozeReplies: (): Promise<void> => Service.SnoozeReplies(),
  getSettings: (): Promise<Settings> => Service.GetSettings() as Promise<Settings>,
  status: (): Promise<Status> => Service.Status(),
  hideCapture: (): Promise<void> => Service.HideCapture(),
  resizeCapture: (height: number): Promise<void> => Service.ResizeCapture(height),
  view: (): Promise<View> => Service.View(),
  done: (id: string): Promise<void> => Service.Done(id),
  reopen: (id: string): Promise<void> => Service.Reopen(id),
  snooze: (id: string, preset: SnoozePreset): Promise<void> => Service.Snooze(id, preset),
  deleteItem: (id: string): Promise<void> => Service.Delete(id),
  /** Undoes deleteItem, restoring the item's status and snooze. */
  undelete: (id: string): Promise<void> => Service.Undelete(id),
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
  /** Voice dictation (internal/app/voice.go). */
  voice: {
    status: (): Promise<VoiceStatus> => VoiceService.Status(),
    install: (): Promise<void> => VoiceService.Install(),
    cancelInstall: (): Promise<void> => VoiceService.CancelInstall(),
    warm: (): Cancellable<void> => VoiceService.Warm(),
    transcribe: (pcm: Int16Array): Cancellable<string> => VoiceService.Transcribe(pcmBase64(pcm)),
  },
  /** Closes the window this page runs in. */
  closeWindow: (): Promise<void> => Window.Close(),
  /** Detail panel (Phase 1.6). */
  itemDetail: (id: string): Promise<Detail> => Service.ItemDetail(id),
  setText: (id: string, text: string): Promise<Item> => Service.SetText(id, text),
  setPriority: (id: string, p: string): Promise<Item> => Service.SetPriority(id, p),
  /** null turns the item into a to-do. */
  setDue: (id: string, due: Date | null): Promise<Item> => Service.SetDue(id, due ? due.toISOString() : null),
  setRepeat: (id: string, repeat: string): Promise<Item> => Service.SetRepeat(id, repeat),
  setTags: (id: string, tags: string[]): Promise<Item> => Service.SetTags(id, tags),
  setDescription: (id: string, md: string): Promise<Item> => Service.SetDescription(id, md),
  /** Stores a compressed WebP image for an item (Go accepts WebP up to 2 MB). */
  addAttachment: async (itemId: string, webp: Blob): Promise<Attachment> => Service.AddAttachment(itemId, await blobBase64(webp)),
  /** Opens an http, https or mailto link in the system browser. */
  openURL: (url: string): Promise<void> => Service.OpenURL(url),
  widenList: (minWidth: number): Promise<void> => Service.WidenList(minWidth),
  restoreListWidth: (): Promise<void> => Service.RestoreListWidth(),
  /** Sync through the user's Atlas cluster (internal/app/syncctl.go). */
  sync: {
    /** Checks, saves and starts syncing; rejects with user-facing text. Returns before the first sync. */
    connect: async (uri: string): Promise<SyncStatus> => syncStatus(await Service.SyncConnect(uri)),
    /** Stops syncing and forgets the connection string; local data stays. */
    disconnect: (): Promise<void> => Service.SyncDisconnect(),
    status: async (): Promise<SyncStatus> => syncStatus(await Service.SyncStatus()),
    /** "Sync now": runs at once and resets the backoff. */
    now: (): Promise<void> => Service.SyncNow(),
    /** attachmentBytes is -1 when unknown (offline or not connected). */
    info: (): Promise<SyncInfo> => Service.SyncInfo(),
  },
};

/** Event names emitted by Go (internal/app/events.go). */
export const events = {
  itemsChanged: "items:changed",
  alertsShow: "alerts:show",
  listFocus: "list:focus",
  captureOpen: "capture:open",
  voiceStatus: "voice:status",
  syncStatus: "sync:status",
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

/** Subscribes to sync status changes; returns the unsubscribe function. */
export function onSyncStatus(cb: (s: SyncStatus) => void): () => void {
  return on<GenSyncStatus>(events.syncStatus, (s) => cb(syncStatus(s)));
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

/** Whether we run on Windows (drive-letter paths). */
export function isWindows(): boolean {
  return System.IsWindows();
}

/** Message text of a rejected binding call. */
export function errorText(err: unknown): string {
  if (err instanceof Error) return err.message;
  if (typeof err === "object" && err !== null && "message" in err) return String((err as { message: unknown }).message);
  return String(err);
}
