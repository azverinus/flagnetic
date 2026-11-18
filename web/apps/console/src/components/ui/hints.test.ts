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

/** The text of each message literal inside `value`, found at `line`. */
function literals(value: string, line: number): { line: number; text: string }[] {
  return [...value.matchAll(/<Trans>([\s\S]*?)<\/Trans>|\bt`([\s\S]*?)`/g)].map((literal) => ({
    line,
    text: (literal[1] ?? literal[2] ?? "").replace(/\s+/g, " ").trim(),
  }));
}

/**
 * The text of each message literal inside the value of every `hint=` prop
 * of a file, and inside every paragraph a control names as its hint by an
 * id ending in `-hint`: a screen that lays out a switch by hand still owes
 * it a one-sentence hint.
 */
function hints(file: string): { line: number; text: string }[] {
  const code = readFileSync(file, "utf8");
  const found: { line: number; text: string }[] = [];
  for (const match of code.matchAll(/<p id=\{`\$\{\w+\}-hint`\}[^>]*>([\s\S]*?)<\/p>/g)) {
    found.push(...literals(match[1] ?? "", code.slice(0, match.index).split("\n").length));
  }
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
    found.push(...literals(value, line));
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

  it("ends without a full stop", () => {
    const stopped = all.filter((hint) => /\.$/.test(hint.text));
    expect(stopped.map((hint) => `${hint.file}:${hint.line} ${hint.text}`)).toEqual([]);
  });

  it("is one sentence", () => {
    const several = all.filter((hint) => /[.!?]\s+[A-Z“"]/.test(hint.text));
    expect(several.map((hint) => `${hint.file}:${hint.line} ${hint.text}`)).toEqual([]);
  });
});
