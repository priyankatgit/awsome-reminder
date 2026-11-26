import "./fonts.css";
import "./tokens.css";
import "./style.css";
import { hasNativeBackdrop } from "./api";
import { mountCapture } from "./capture";

// One page, one view per window, chosen by location.hash.
// Later tasks add "list", "alert" and "settings".
const routes: Record<string, (root: HTMLElement) => void> = {
  capture: mountCapture,
};

const root = document.getElementById("app");
const route = routes[location.hash.replace(/^#/, "")];
if (root && route) {
  document.documentElement.dataset.backdrop = hasNativeBackdrop() ? "glass" : "solid";
  route(root);
}
