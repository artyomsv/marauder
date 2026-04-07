import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import path from "node:path";

// Vitest config kept separate from vite.config.ts so the production
// build pipeline isn't burdened with test-only plugins or settings.
// We intentionally omit the @tailwindcss/vite plugin here — tests run
// in jsdom and don't need real CSS processing, and css: true below
// just tells vitest to parse (not transform) imported stylesheets so
// `import "./foo.css"` statements don't blow up.
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  test: {
    globals: true,
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    css: true,
    include: ["src/**/*.{test,spec}.{ts,tsx}"],
  },
});
