// Dev-only fake SetupService for previewing the setup screen in a plain
// browser (`npm --prefix frontend run dev`, then open
// http://127.0.0.1:9245/?demo#setup  (existing data; same as ?demo=existing),
// ?demo=fresh#setup, ?demo=unavailable#setup or ?demo=move#setup).
// Browse cycles through sample folders: E:\Notes (clean), a OneDrive folder
// (cloud warning), D:\data (holds data: adopt confirm), a UNC path (network
// warning), E:\full (not enough space), the standard folder (already there).
// Check says hasData for a path containing "data", cloud for "OneDrive". A
// move ticks through the four steps in about 5 s; a path containing "fail"
// makes it fail. Loaded only when import.meta.env.DEV and ?demo are both set,
// so production bundles never include it.
import { api, emitLocal, events, type SetupCheck, type SetupInfo } from "./api";

const STANDARD = "C:\\Users\\you\\AppData\\Roaming\\awesome-reminder";
const GB = 1024 ** 3;

const PICKS = [
  "E:\\Notes\\Awesome Reminder",
  "C:\\Users\\you\\OneDrive\\Notes",
  "D:\\data",
  "\\\\server\\share\\reminder",
  "E:\\full",
  STANDARD,
];

function demoInfo(mode: string): SetupInfo {
  const options = [
    { kind: "standard", label: mode === "fresh" ? "Standard location" : "Keep it where it is", path: STANDARD, free: 41 * GB, freeText: "41.0 GB free" },
    { kind: "beside", label: "Next to the app", path: "D:\\program\\awesome-reminder\\Data", free: 312 * GB, freeText: "312.0 GB free" },
  ];
  const base = { state: mode, root: "", reason: "", existingDir: "", options, moveFrom: "", moveTo: "", needText: "" };
  switch (mode) {
    case "fresh":
      return base;
    case "unavailable":
      return { ...base, root: "E:\\Notes\\Awesome Reminder", reason: "awesome.db is missing", options: [] };
    case "move":
      return { ...base, root: STANDARD, moveFrom: STANDARD, moveTo: "E:\\Notes\\Awesome Reminder", options: [] };
    default:
      return { ...base, state: "existing", existingDir: STANDARD, needText: "41 MB" };
  }
}

function demoCheck(path: string): SetupCheck {
  const c = { path, free: 120 * GB, need: 41 * 1024 ** 2, hasData: false, network: false, cloud: false, same: false, err: "" };
  if (path.toLowerCase().includes("data")) c.hasData = true;
  if (path.includes("OneDrive")) c.cloud = true;
  if (path.startsWith("\\\\")) c.network = true;
  if (path.includes("full")) return { ...c, free: 12 * 1024 ** 2, err: "Not enough space: needs 230 MB, 12 MB free." };
  if (path === STANDARD) c.same = true;
  return c;
}

export function installSetupDemo(): void {
  const mode = new URLSearchParams(location.search).get("demo") || "existing";
  const info = demoInfo(["fresh", "unavailable", "move"].includes(mode) ? mode : "existing");
  let picks = 0;
  let timer: ReturnType<typeof setInterval> | undefined;

  const steps = ["database", "attachments", "voice", "checking"];
  function fakeMove(to: string): void {
    let tick = 0;
    const total = 5; // ticks per step
    clearInterval(timer);
    timer = setInterval(() => {
      const step = steps[Math.floor(tick / total)];
      emitLocal(events.setupProgress, { step, done: (tick % total) + 1, total });
      tick++;
      if (tick >= steps.length * total) {
        clearInterval(timer);
        emitLocal(events.setupDone, to.includes("fail") ? { ok: false, error: "The copy failed its check: awesome.db has 3 fewer tasks." } : { ok: true, error: "" });
      }
    }, 250);
  }

  Object.assign(api, {
    chooseFolder: async () => {
      await new Promise((r) => setTimeout(r, 150));
      return PICKS[picks++ % PICKS.length];
    },
  });
  Object.assign(api.setup, {
    info: async () => info,
    check: async (path: string) => demoCheck(path),
    choose: async (kind: string, path: string, confirm: boolean) => {
      const c = kind === "other" ? demoCheck(path) : { ...demoCheck(path), hasData: false };
      if (info.state === "unavailable") {
        if (!confirm) throw new Error(c.hasData ? "confirm-adopt" : "confirm-fresh");
        return;
      }
      if (c.hasData && !confirm) throw new Error("confirm-adopt");
      if (info.state === "existing" && kind !== "standard" && !c.hasData) fakeMove(path);
    },
    runPendingMove: async () => fakeMove(info.moveTo),
    cancelMove: async () => {
      clearInterval(timer);
      emitLocal(events.setupDone, { ok: false, error: "context canceled" });
    },
    abandonMove: async () => undefined,
    retry: async () => info,
    quit: async () => undefined,
  });
}
