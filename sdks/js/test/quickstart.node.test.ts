// The quickstart of docs/sdks/javascript.md is code a reader pastes: its
// snippets are compiled against the package's own types and run, the server
// ones against a mock platform and the browser one against a stubbed
// `fetch`, so the page cannot drift from the API it teaches.
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import ts from "typescript";
import { afterAll, afterEach, beforeAll, describe, expect, test, vi } from "vitest";
import { MockPlatform, rulesetOf } from "./support/mock-platform.js";

const packageRoot = fileURLToPath(new URL("..", import.meta.url));
const doc = readFileSync(join(packageRoot, "../../docs/sdks/javascript.md"), "utf8");

/** The `ts` blocks of the Quickstart section, in order. */
function snippets(): string[] {
  const section = doc.split(/^## /m).find((s) => s.startsWith("Quickstart")) ?? "";
  return [...section.matchAll(/^```ts\n([\s\S]*?)^```$/gm)].map((m) => m[1] ?? "");
}

const all = snippets();
const browser = all.filter((s) => s.includes('"flagnetic/client"'));
const serverMode = all.filter((s) => !browser.includes(s));

/** Points the snippets' package imports at this checkout's source. */
function localImports(code: string): string {
  return code
    .replaceAll('"flagnetic/server"', '"../../src/server.js"')
    .replaceAll('"flagnetic/client"', '"../../src/client.js"');
}

const key = "fn_srv_0123456789abcdefghijklmnopqrstuvwxyzABCD";
const clientKey = "fn_cli_0123456789abcdefghijklmnopqrstuvwxyzABCD";

let dir = "";
beforeAll(() => {
  const parent = join(packageRoot, "test");
  mkdirSync(parent, { recursive: true });
  dir = mkdtempSync(join(parent, ".quickstart-"));
});
afterAll(() => {
  rmSync(dir, { recursive: true, force: true });
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
});

function write(name: string, code: string): string {
  const path = join(dir, name);
  writeFileSync(path, code);
  return path;
}

describe("the quickstart", () => {
  test("has a server program and a browser snippet", () => {
    expect(serverMode.length).toBeGreaterThanOrEqual(4);
    expect(browser).toHaveLength(1);
  });

  test("covers install, key, evaluate, context, exposure and shutdown", () => {
    const section = doc.split(/^## /m).find((s) => s.startsWith("Quickstart")) ?? "";
    for (const word of ["npm install flagnetic", "FLAGNETIC_SDK_KEY", "getBool", "units:", "exposure(", "close()"]) {
      expect(section, word).toContain(word);
    }
  });

  test("compiles against the package's types", () => {
    const server = write("server.ts", `${localImports(serverMode.join("\n"))}\nexport {};\n`);
    const client = write("client.ts", `${localImports(browser.join("\n"))}\nexport {};\n`);
    const config = ts.readConfigFile(join(packageRoot, "tsconfig.json"), (path) => ts.sys.readFile(path));
    const options = ts.parseJsonConfigFileContent(config.config, ts.sys, packageRoot).options;
    const program = ts.createProgram([server, client], { ...options, noEmit: true });
    const diagnostics = ts.getPreEmitDiagnostics(program).filter((d) => d.file?.fileName.startsWith(dir) === true);
    expect(diagnostics.map((d) => ts.flattenDiagnosticMessageText(d.messageText, "\n"))).toStrictEqual([]);
  });

  test("the server program runs against a platform and ends by closing the client", async () => {
    const p = await MockPlatform.start(rulesetOf(1, { new_checkout: true }));
    try {
      vi.stubEnv("FLAGNETIC_SDK_KEY", key);
      const logged = vi.spyOn(console, "log").mockImplementation(() => undefined);
      const code = localImports(serverMode.join("\n")).replaceAll("https://flags.example.com", p.baseUrl);
      await import(/* @vite-ignore */ pathToFileURL(write("server-run.ts", code)).href);
      expect(logged).toHaveBeenCalledWith("fresh", true, "Welcome", "fallback");
      logged.mockRestore();
      expect(p.rulesetRequests().length).toBeGreaterThan(0);
      // close() sent what was queued: the alias.
      expect(p.events().map((e) => e.type)).toContain("alias");
    } finally {
      await p.stop();
    }
  });

  test("the browser snippet runs and evaluates against the server", async () => {
    const requests: string[] = [];
    vi.stubGlobal("fetch", (input: string | URL | Request) => {
      requests.push(typeof input === "string" ? input : input instanceof URL ? input.href : input.url);
      return Promise.resolve(
        new Response(
          JSON.stringify({
            schema_version: 1,
            ruleset_version: 1,
            environment: { key: "development", protected: false },
            flags: { new_checkout: { type: "boolean", value: true, variation: "true", reason: "targeting_match", override_influenced: false, automatic_exposure: false } },
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      );
    });
    const code = `${localImports(browser.join("\n")).replaceAll("fn_cli_…", clientKey)}\nexport { browser as started };\n`;
    const mod = (await import(/* @vite-ignore */ pathToFileURL(write("client-run.ts", code)).href)) as {
      started: { ready(t?: number): Promise<string>; getBool(k: string, d: boolean): boolean; close(): Promise<void> };
    };
    expect(await mod.started.ready(2000)).toBe("fresh");
    expect(mod.started.getBool("new_checkout", false)).toBe(true);
    expect(requests.some((u) => u.endsWith("/sdk/v1/evaluate"))).toBe(true);
    await mod.started.close();
  });
});

