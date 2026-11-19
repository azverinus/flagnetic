import { resolve } from "node:path";

import { ESLint } from "eslint";
import { describe, expect, it } from "vitest";

// The lint rule that keeps width, page padding and casing out of screens
// runs here over a failing and a passing screen, so a change to the
// configuration that loosens it fails a test, not a review.
const web = resolve(process.cwd(), "../..");
const eslint = new ESLint({ cwd: web });

async function messages(source: string, file: string) {
  const [result] = await eslint.lintText(source, {
    filePath: `${web}/apps/console/src/${file}`,
  });
  return result.messages.filter((m) => m.ruleId === "no-restricted-syntax");
}

const failing = `
export function Screen() {
  return <div className="mx-auto max-w-4xl p-6 uppercase" />;
}
`;

const passing = `
import { Card } from "@/components/ui/card";
import { Page } from "@/components/ui/page";

export function Screen() {
  return (
    <Page measure="form">
      <Card className="max-w-full! min-w-0 p-4 max-sm:p-3" />
    </Page>
  );
}
`;

describe("screen class names", () => {
  it("refuses a hand-written width, centring, page padding and uppercase, naming the primitive", async () => {
    const found = await messages(failing, "routes/fixture.tsx");
    expect(found.map((m) => m.message).join("\n")).toMatch(/<Page measure=/);
    expect(found.map((m) => m.message).join("\n")).toMatch(/<Page> owns it/);
    expect(found.map((m) => m.message).join("\n")).toMatch(/no uppercase/);
  });

  it("refuses them with a variant prefix and in a template literal", async () => {
    const found = await messages(
      "export const a = <div className={`flex md:max-w-2xl max-sm:px-6`} />;",
      "components/fixture.tsx",
    );
    expect(found).toHaveLength(2);
  });

  it("refuses them with an arbitrary variant or the important modifier", async () => {
    const found = await messages(
      'export const a = <div className="data-[state=open]:mx-auto p-6! !uppercase" />;',
      "components/fixture.tsx",
    );
    expect(found).toHaveLength(3);
  });

  it("accepts a screen built from the primitives", async () => {
    expect(await messages(passing, "routes/fixture.tsx")).toEqual([]);
  });

  it("leaves the kit, where these choices live, alone", async () => {
    expect(await messages(failing, "components/ui/fixture.tsx")).toEqual([]);
  });
});
