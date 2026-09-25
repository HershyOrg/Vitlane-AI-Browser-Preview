import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";

export default defineConfig({
  define: {
    "process.env.NODE_ENV": JSON.stringify("production"),
  },
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  build: {
    copyPublicDir: false,
    cssCodeSplit: false,
    emptyOutDir: true,
    lib: {
      entry: fileURLToPath(
        new URL("./src/marketing/main.tsx", import.meta.url),
      ),
      cssFileName: "standard-stack",
      fileName: "standard-stack",
      formats: ["es"],
    },
    outDir: "../marketing/assets/generated",
  },
});
