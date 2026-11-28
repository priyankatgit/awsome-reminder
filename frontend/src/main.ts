import "./fonts.css";
import "./tokens.css";
import "./style.css";
import { hasNativeBackdrop } from "./api";
import { mountAlert } from "./alert";
import { mountCapture } from "./capture";
import { mountList } from "./list";
import { mountSettings } from "./settings";
import { mountSetup } from "./setup";

// One page, one view per window, chosen by location.hash.
const routes: Record<string, (root: HTMLElement) => void> = {
  capture: mountCapture,
  list: mountList,
  alert: mountAlert,
  settings: mountSettings,
  setup: mountSetup,
};

// The webview never navigates: a link or text dragged in from outside (or
// from our own page) and dropped anywhere but an editable field would
// otherwise load it in place of the app. File drags are the Wails runtime's
// (it stops those itself); the description editor takes its own drops first.
const TEXT_FIELD =
  "textarea, [contenteditable]:not([contenteditable='false']), " +
  "input:not([type=checkbox], [type=radio], [type=button], [type=submit], [type=reset], [type=file], [type=range], [type=color], [type=image])";
const editable = (t: EventTarget | null) => t instanceof Element && t.closest(TEXT_FIELD) !== null;
for (const type of ["dragover", "drop"] as const) {
  document.addEventListener(type, (e) => {
    if (e.defaultPrevented || editable(e.target)) return;
    e.preventDefault();
    if (e.dataTransfer) e.dataTransfer.dropEffect = "none";
  });
}

const root = document.getElementById("app");
const route = routes[location.hash.replace(/^#/, "")];
async function start(): Promise<void> {
  if (!root || !route) return;
  document.documentElement.dataset.backdrop = hasNativeBackdrop() ? "glass" : "solid";
  // Dev-only sample data for a plain-browser preview; dropped from production builds.
  if (import.meta.env.DEV && new URLSearchParams(location.search).has("demo")) {
    if (location.hash === "#setup") (await import("./setup-demo")).installSetupDemo();
    else (await import("./list-demo")).installDemo();
  }
  route(root);
}
void start();
