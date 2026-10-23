// Checks the built package the way a consumer loads it: every entry point of
// the exports map resolves through the package's own name from both ESM and
// CommonJS, with type declarations beside it, the server entry evaluates a
// flag in both formats, the browser entry never carries the evaluator, and
// the package has no runtime dependencies. Runs with no dev dependencies,
// and CI runs it once more with `react` and the OpenFeature SDKs removed,
// which is what proves the other entry points load without their optional
// peer dependencies.
import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { createRequire } from "node:module";

const pkg = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8"));
const require = createRequire(import.meta.url);

for (const field of ["dependencies", "optionalDependencies", "bundleDependencies"]) {
  assert.equal(pkg[field], undefined, `package.json must not declare ${field}`);
}

// Three peer dependencies, all optional: flagnetic/react needs react,
// flagnetic/openfeature needs both OpenFeature SDKs, every other entry point
// loads without any of them.
assert.deepEqual(Object.keys(pkg.peerDependencies).sort(), ["@openfeature/server-sdk", "@openfeature/web-sdk", "react"]);
for (const name of Object.keys(pkg.peerDependencies)) {
  assert.equal(pkg.peerDependenciesMeta[name].optional, true, `${name} must be an optional peer`);
}

const read = (file) => readFileSync(new URL(`../${file}`, import.meta.url), "utf8");

const entries = Object.keys(pkg.exports).filter((e) => e !== "./package.json");
assert.deepEqual(entries.sort(), [".", "./client", "./openfeature", "./react", "./server"]);

for (const entry of entries) {
  const specifier = entry === "." ? pkg.name : `${pkg.name}/${entry.slice(2)}`;
  const target = pkg.exports[entry];
  for (const condition of ["import", "require"]) {
    for (const file of [target[condition].types, target[condition].default]) {
      assert.ok(existsSync(new URL(`../${file}`, import.meta.url)), `${specifier} (${condition}): ${file} is missing`);
    }
  }
  // Both formats of every entry point load, and a named export is the same
  // in both.
  const esm = await import(specifier);
  const cjs = require(specifier);
  assert.deepEqual(Object.keys(esm).filter((k) => k !== "default").sort(), Object.keys(cjs).sort(), `${specifier}: ESM and CJS export different names`);
}

// The server entry evaluates a flag, in both formats.
const doc = {
  schema_version: 1,
  version: 1,
  scope: "server",
  flags: [
    { key: "f", type: "boolean", automatic_exposure: true, on: false, off_variation: "off", variations: [{ key: "off", value: false }] },
  ],
};
// The result carries the flag's type and its automatic-exposure setting, so
// the SDK knows from the result alone whether to record an exposure.
const want = { value: false, variation: "off", reason: "off", override_influenced: false, type: "boolean", automatic_exposure: true };
const esm = await import(`${pkg.name}/server`);
const cjs = require(`${pkg.name}/server`);
assert.deepEqual(esm.decodeRuleset(doc).evaluate("f", {}), want);
assert.deepEqual(cjs.decodeRuleset(doc).evaluate("f", {}), want);
assert.equal(esm.bucket("a3f9c2e17b4d0e58", "u_10482"), cjs.bucket("a3f9c2e17b4d0e58", "u_10482"));
assert.equal(esm.VERSION, pkg.version);

// The browser entry point never carries the evaluator: not the hash, the
// decoder, the regular expression engine or its Unicode tables.
const evaluator = ["murmur3", "decodeRuleset", "compileRE2", "unicodeClasses", "compileOperator"];
for (const file of ["dist/client.js", "dist/client.cjs"]) {
  const code = read(file);
  for (const marker of evaluator) {
    assert.ok(!code.includes(marker), `${file} bundles the evaluator (${marker})`);
  }
}
// The marker list itself is proven by the server entry, which has them all.
for (const file of ["dist/server.js", "dist/server.cjs"]) {
  const code = read(file);
  for (const marker of evaluator) {
    assert.ok(code.includes(marker), `${file} does not contain ${marker}: the evaluator markers of this check are stale`);
  }
}

// Only flagnetic/react reaches for React, and it never bundles it.
for (const name of ["index", "client", "server", "openfeature"]) {
  for (const file of [`dist/${name}.js`, `dist/${name}.cjs`]) {
    assert.ok(!/["']react["']/.test(read(file)), `${file} pulls in react, which is an optional peer dependency`);
  }
}

// Only flagnetic/openfeature reaches for the OpenFeature SDKs, and it never
// bundles them.
for (const name of ["index", "client", "server", "react"]) {
  for (const file of [`dist/${name}.js`, `dist/${name}.cjs`]) {
    assert.ok(!read(file).includes("@openfeature/"), `${file} pulls in an OpenFeature SDK, which is only flagnetic/openfeature's optional peer`);
  }
}

const resolvable = (name) => {
  try {
    require.resolve(name);
    return true;
  } catch {
    return false;
  }
};
const react = resolvable("react") ? "react installed" : "react absent";
const openfeature = resolvable("@openfeature/server-sdk") || resolvable("@openfeature/web-sdk") ? "OpenFeature installed" : "OpenFeature absent";
console.log(`ok: ${entries.length} entry points, ESM and CJS, no runtime dependencies, ${react}, ${openfeature}, every entry point loaded (Node ${process.version})`);
