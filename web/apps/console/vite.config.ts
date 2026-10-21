import { writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { lingui } from "@lingui/vite-plugin";
import tailwindcss from "@tailwindcss/vite";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

/** Where the Go binary embeds the build from. */
const outDir = fileURLToPath(
  new URL("../../../internal/infra/in/http/console/dist", import.meta.url),
);

/**
 * Puts the marker back after a build has emptied the directory. The Go
 * binary embeds the directory, so it must exist in a fresh clone for
 * `go build` to work without Node; the marker is the only file committed
 * there.
 */
function keepEmbeddedDirectory() {
  return {
    name: "flagnetic:keep-embedded-directory",
    closeBundle() {
      writeFileSync(resolve(outDir, ".gitkeep"), "");
    },
  } as const;
}

/**
 * The console is served from the origin root by the Go binary, which
 * embeds this build. In development it runs on its own port and proxies the
 * management API to a running binary, so the session cookie stays
 * same-origin.
 */
export default defineConfig({
  plugins: [
    tanstackRouter({ target: "react", autoCodeSplitting: true }),
    react(),
    lingui({ macroTransform: true }),
    tailwindcss(),
    keepEmbeddedDirectory(),
  ],
  resolve: {
    alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) },
  },
  build: {
    outDir,
    emptyOutDir: true,
    sourcemap: false,
  },
  server: {
    port: 5173,
    proxy: {
      "/api": { target: process.env.FLAGNETIC_URL ?? "http://localhost:8080", changeOrigin: false },
    },
  },
});
