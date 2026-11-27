// Unit tests only (`npm test`). Vitest reads this file instead of
// vite.config.ts, and `vite build` never reads it, so nothing here reaches the
// production bundle. Test files are not imported by any entry point, so Vite
// never bundles them either.
import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    environment: "jsdom",
    include: ["src/**/*.test.ts"],
  },
});
