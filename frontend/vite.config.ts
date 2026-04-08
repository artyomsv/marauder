import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "node:path";

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    // Host port stays in the 34xxx range per local-port-ranges rule
    // (host-exposed ports must be 30000-49999). 34000 = Marauder
    // frontend dev server. Production Vite preview / nginx still
    // listens on 8081 inside the container, mapped to a 34xxx host
    // port via the gateway.
    host: "0.0.0.0",
    port: 34000,
    proxy: {
      "/api": {
        // Default points at the dev compose backend host port (34081),
        // which is what `docker compose -f docker-compose.yml -f
        // docker-compose.dev.yml up` publishes.
        target: process.env.VITE_API_URL || "http://localhost:34081",
        changeOrigin: true,
        secure: false,
      },
    },
  },
  build: {
    outDir: "dist",
    sourcemap: false,
    target: "es2022",
  },
});
