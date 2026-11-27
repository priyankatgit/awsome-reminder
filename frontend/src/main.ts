import "./fonts.css";
import "./tokens.css";
import "./style.css";
import { hasNativeBackdrop } from "./api";
import { mountAlert } from "./alert";
import { mountCapture } from "./capture";
import { mountList } from "./list";
import { mountSettings } from "./settings";

// One page, one view per window, chosen by location.hash.
const routes: Record<string, (root: HTMLElement) => void> = {
  capture: mountCapture,
  list: mountList,
  alert: mountAlert,
  settings: mountSettings,
};

const root = document.getElementById("app");
const route = routes[location.hash.replace(/^#/, "")];
async function start(): Promise<void> {
  if (!root || !route) return;
  document.documentElement.dataset.backdrop = hasNativeBackdrop() ? "glass" : "solid";
  // Dev-only sample data for a plain-browser preview; dropped from production builds.
  if (import.meta.env.DEV && new URLSearchParams(location.search).has("demo")) {
    (await import("./list-demo")).installDemo();
  }
  route(root);
}
void start();
