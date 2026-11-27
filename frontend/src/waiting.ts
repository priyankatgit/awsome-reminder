// Replies owed as the list and the alert cards show them: how long someone
// has been waiting. Pure helpers, no DOM.

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

function startOfDay(d: Date): number {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
}

/**
 * How long a reply has waited: "5 min" (at least 1), "2 h" under a day, then
 * by calendar date "yesterday" or "3 days".
 */
export function ageLabel(created: Date, now: Date): string {
  const ms = now.getTime() - created.getTime();
  if (ms < HOUR) return `${Math.max(1, Math.floor(ms / MINUTE))} min`;
  if (ms < DAY) return `${Math.floor(ms / HOUR)} h`;
  const days = Math.max(1, Math.round((startOfDay(now) - startOfDay(created)) / DAY)); // DST-safe
  return days === 1 ? "yesterday" : `${days} days`;
}

/** Whether a reply has waited more than 24 hours (its age reads in --time-text). */
export function isStale(created: Date, now: Date): boolean {
  return now.getTime() - created.getTime() > DAY;
}
