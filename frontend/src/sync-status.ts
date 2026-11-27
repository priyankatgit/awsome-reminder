// Pure helpers for the list window's sync pill (sync-pill.ts renders them).
// Wording follows internal/app/synctext.go and the status vocabulary in the
// Phase 2 plan.
import type { SyncStatus } from "./api";

export type Tone = "ok" | "neutral" | "muted" | "danger";
export interface PillModel {
  text: string;
  icon: "cloud" | "cloudCheck" | "cloudOff" | "cloudAlert" | "refresh";
  tone: Tone;
  spin: boolean;
}

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;
const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** "just now" (< 1 min), "N min ago", "N h ago" (< 24 h), else "on 26 Nov". Same as Go relTime. */
export function relTime(now: Date, t: Date): string {
  const d = now.getTime() - t.getTime();
  if (d < MINUTE) return "just now";
  if (d < HOUR) return `${Math.floor(d / MINUTE)} min ago`;
  if (d < DAY) return `${Math.floor(d / HOUR)} h ago`;
  return `on ${t.getDate()} ${MONTHS[t.getMonth()]}`;
}

function changes(n: number): string {
  return n === 1 ? "1 change" : `${n} changes`;
}

/** What the pill shows; null when sync is off (no pill at all). */
export function pillModel(st: SyncStatus, now: Date): PillModel | null {
  switch (st.state) {
    case "syncing":
      return { text: "Syncing…", icon: "refresh", tone: "neutral", spin: true };
    case "synced":
      if (st.pending > 0) return { text: `Synced · ${st.pending} waiting`, icon: "cloud", tone: "neutral", spin: false };
      return {
        text: st.lastSyncAt ? `All synced · ${relTime(now, new Date(st.lastSyncAt))}` : "All synced",
        icon: "cloudCheck",
        tone: "ok",
        spin: false,
      };
    case "offline":
      return {
        text: st.pending > 0 ? `Offline · ${changes(st.pending)} waiting` : "Offline",
        icon: "cloudOff",
        tone: "muted",
        spin: false,
      };
    case "error":
      return { text: "Sync error", icon: "cloudAlert", tone: "danger", spin: false };
  }
  return null;
}

/** One glow when the last waiting change reaches Atlas. */
export function shouldGlow(prev: SyncStatus | null, next: SyncStatus): boolean {
  return prev !== null && prev.pending > 0 && next.pending === 0 && next.state === "synced";
}

/** Rows carry "not synced yet" markers whenever sync is configured. */
export function showMarkers(st: SyncStatus): boolean {
  return st.state !== "off";
}

/**
 * Whether the list must refetch View (its pending ids) after a status change.
 * The count alone is not enough: one change may sync while another is added.
 */
export function needsRefetch(prev: SyncStatus | null, next: SyncStatus): boolean {
  if (prev === null) return true;
  return (
    prev.pending !== next.pending ||
    prev.lastSyncAt !== next.lastSyncAt ||
    prev.state === "off" ||
    (prev.state === "syncing" && next.state !== "syncing")
  );
}
