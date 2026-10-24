// Holds flagnetic/client to its size budget: what a page pays for the
// browser client, measured the way a bundler ships it, minified and then
// gzipped. The budget is the first build's size plus 10 %. Raise it only in
// a commit of its own that says what grew and why it is worth the bytes.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { gzipSync } from "node:zlib";
import { minify } from "vite";

/** Bytes of dist/client.js minified and gzipped: 10 369 at the first build, plus 10 %. */
const BUDGET = 11_406;

const code = readFileSync(new URL("../dist/client.js", import.meta.url), "utf8");
const { code: minified } = await minify("client.js", code, { compress: true, mangle: true });
const size = gzipSync(minified).length;
assert.ok(size <= BUDGET, `flagnetic/client is ${size} bytes minified and gzipped, over its budget of ${BUDGET}`);
console.log(`ok: flagnetic/client is ${size} bytes minified and gzipped, budget ${BUDGET} (${Math.round((size / BUDGET) * 100)} %)`);
