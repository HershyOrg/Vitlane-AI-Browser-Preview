import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";

const apiProxyTarget =
  process.env.VITE_API_PROXY_TARGET ?? "http://127.0.0.1:8080";

// Design Lab and the curation studio are review tools. A dev-auth build (local
// review, previews) emits them so the server can serve them next to the app;
// a release build (VITE_ALLOW_DEV_AUTH_UI=false) stays single-page.
const reviewPages =
  process.env.VITE_ALLOW_DEV_AUTH_UI === "true"
    ? {
        designLab: fileURLToPath(new URL("./design-lab.html", import.meta.url)),
        curationStudio: fileURLToPath(new URL("./curation-studio.html", import.meta.url)),
      }
    : {};

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  server: {
    port: 5173,
    allowedHosts: ["localhost", "127.0.0.1", "host.docker.internal"],
    proxy: {
      "/api": apiProxyTarget,
      "/livez": apiProxyTarget,
      "/readyz": apiProxyTarget,
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    rollupOptions: {
      input: {
        index: fileURLToPath(new URL("./index.html", import.meta.url)),
        ...reviewPages,
      },
    },
  },
  test: {
    setupFiles: ["./src/test/setup.ts"],
  },
});
