import { readFileSync } from "node:fs";
import { join } from "node:path";

import { describe, expect, it } from "vitest";

/**
 * The Russian catalog follows the glossary beside it (`GLOSSARY.md`): one
 * word per term. These are the words the glossary retires, each replaced by
 * the word its tables name, so a string that brings one back fails here and
 * not in review. The Russian mail templates keep the same list in the mail
 * package's tests.
 */
const retiredWords =
  /экземпляр|экспозиц|сеанс|корзин|аварийн|сопровождающ|перебазир|ретеншен|вариаци|раскатк|переопредел|предуслов|набор\w* правил|\(а\)|единиц/i;

/** «единица» is a unit of time in these messages and stays so. */
const unitOfTimeMessages = new Set([
  "Unit of the maximum token lifetime",
  "Unit of the session absolute timeout",
  "Unit of the session idle timeout",
]);

interface Entry {
  id: string;
  translation: string;
}

/** The quoted lines that follow a keyword, joined and unescaped. */
function field(block: string, keyword: string): string | undefined {
  const lines = block.split("\n");
  const start = lines.findIndex((line) => line.startsWith(`${keyword} "`));
  if (start === -1) return undefined;
  const parts: string[] = [];
  for (let i = start; i < lines.length; i++) {
    const line = lines[i]!;
    if (i > start && !line.startsWith('"')) break;
    parts.push(JSON.parse(line.slice(line.indexOf('"'))) as string);
  }
  return parts.join("");
}

function readCatalog(): Entry[] {
  const source = readFileSync(join(process.cwd(), "src/locales/ru/messages.po"), "utf8");
  return source
    .split("\n\n")
    .map((block) => ({ id: field(block, "msgid"), translation: field(block, "msgstr") }))
    .filter((entry): entry is Entry => entry.id !== undefined && entry.id !== "");
}

describe("the Russian catalog", () => {
  const catalog = readCatalog();

  it("is read whole and translated", () => {
    expect(catalog.length).toBeGreaterThan(2000);
    expect(catalog.filter((entry) => entry.translation === "")).toEqual([]);
  });

  it("uses no word the glossary retires", () => {
    const offenders = catalog
      .filter((entry) => !unitOfTimeMessages.has(entry.id))
      .filter((entry) => retiredWords.test(entry.translation))
      .map((entry) => entry.id);
    expect(offenders).toEqual([]);
  });

  it("keeps «единица» only for the three units of time", () => {
    const found = catalog.filter((entry) => /единиц/i.test(entry.translation)).map((e) => e.id);
    expect(new Set(found)).toEqual(unitOfTimeMessages);
  });
});
