import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

const src = join(dirname(fileURLToPath(import.meta.url)), "..", "..");

function sources(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) return sources(path);
    return /\.tsx$/.test(name) && !/\.test\./.test(name) ? [path] : [];
  });
}

/** The text of each message literal inside the value of every `hint=` prop of a file. */
function hints(file: string): { line: number; text: string }[] {
  const code = readFileSync(file, "utf8");
  const found: { line: number; text: string }[] = [];
  for (const match of code.matchAll(/\bhint=\{/g)) {
    let depth = 1;
    let end = match.index + match[0].length;
    while (end < code.length && depth > 0) {
      if (code[end] === "{") depth++;
      else if (code[end] === "}") depth--;
      end++;
    }
    const value = code.slice(match.index + match[0].length, end - 1);
    const line = code.slice(0, match.index).split("\n").length;
    for (const literal of value.matchAll(/<Trans>([\s\S]*?)<\/Trans>|\bt`([\s\S]*?)`/g)) {
      const text = (literal[1] ?? literal[2] ?? "").replace(/\s+/g, " ").trim();
      found.push({ line, text });
    }
  }
  return found;
}

describe("a field's hint", () => {
  const all = sources(src).flatMap((file) =>
    hints(file).map((hint) => ({ ...hint, file: file.slice(src.length + 1) })),
  );

  it("is found in the console's screens", () => {
    expect(all.length).toBeGreaterThan(50);
  });

  it("is at most 120 characters in English", () => {
    const tooLong = all.filter((hint) => hint.text.length > 120);
    expect(tooLong.map((hint) => `${hint.file}:${hint.line} (${hint.text.length})`)).toEqual([]);
  });

  it("is one sentence", () => {
    const several = all.filter((hint) => /[.!?]\s+[A-Z“"]/.test(hint.text));
    expect(several.map((hint) => `${hint.file}:${hint.line} ${hint.text}`)).toEqual([]);
  });
});
