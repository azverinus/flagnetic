import { defineConfig } from "tsup";

// One package, one file per entry point, ESM and CJS side by side with
// bundled declarations (.d.ts for import, .d.cts for require).
export default defineConfig({
  entry: {
    index: "src/index.ts",
    client: "src/client.ts",
    server: "src/server.ts",
    react: "src/react.ts",
    openfeature: "src/openfeature.ts",
  },
  format: ["esm", "cjs"],
  dts: true,
  target: "es2022",
  platform: "neutral",
  splitting: false,
  sourcemap: true,
  clean: true,
  treeshake: true,
  // `react`, `@openfeature/server-sdk` and `@openfeature/web-sdk` are
  // optional peer dependencies: never bundled, never pulled into an entry
  // point that does not name them.
  external: ["react", "@openfeature/server-sdk", "@openfeature/web-sdk"],
});
