// Holds flagnetic/client to its size budget: what a page pays for the
// browser client, measured the way a bundler ships it, minified and then
// gzipped. The budget is the first build's size plus 10 %, raised to cover
// the guard that keeps a previous context's assignments from being exposed,
// the depth bound and stored-data clearing, then to 12 500 for the summary
// window kept in the queue, the catch-up retries after an answer below an
// announced version and the summary interval, then to 12 600 for the rejected
// events read from a 202 answer and the rule that reopens a refused stream, then to 12 700 for the refusal
// of an SDK key no header can carry and the request failure cause that never repeats a header value. Raise it only in a commit of
// its own that says what grew and why it is worth the bytes.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { gzipSync } from "node:zlib";
import { minify } from "vite";

/** Bytes of dist/client.js minified and gzipped: 10 369 at the first build, plus 10 %, raised as the header says, now 12 600. */
const BUDGET = 12_700;

const code = readFileSync(new URL("../dist/client.js", import.meta.url), "utf8");
const { code: minified } = await minify("client.js", code, { compress: true, mangle: true });
const size = gzipSync(minified).length;
assert.ok(size <= BUDGET, `flagnetic/client is ${size} bytes minified and gzipped, over its budget of ${BUDGET}`);
console.log(`ok: flagnetic/client is ${size} bytes minified and gzipped, budget ${BUDGET} (${Math.round((size / BUDGET) * 100)} %)`);
