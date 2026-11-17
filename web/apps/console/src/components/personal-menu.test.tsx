import type { Me } from "@flagnetic/api";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { recentContexts, rememberContext } from "@/lib/tester";
import { fakeServer } from "@/test/fake-server";
import { activateForTests, renderWithRouter } from "@/test/render";

import { PersonalMenu } from "./personal-menu";

const me = {
  credential: { token_name: "" },
  member: { id: "m1", display_name: "Grace Hopper", email: "grace@flagnetic.test", locale: null },
} as unknown as Me;

async function signOutFromMenu() {
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Grace Hopper" }));
  await user.click(await screen.findByRole("menuitem", { name: "Sign out" }));
}

describe("signing out from the personal menu", () => {
  beforeEach(() => {
    activateForTests("en");
    localStorage.clear();
    // The theme the menu offers follows the system's, which jsdom does not have.
    window.matchMedia = ((query: string) =>
      ({
        matches: false,
        media: query,
        addEventListener: () => {},
        removeEventListener: () => {},
      }) as unknown as MediaQueryList) as typeof window.matchMedia;
  });
  afterEach(() => localStorage.clear());

  it("forgets the tester's recent contexts, so whoever signs in next does not find them", async () => {
    const server = fakeServer({
      "GET /me": { status: 200, body: me },
      "POST /auth/sign-out": { status: 204 },
    });
    rememberContext("checkout", { units: { user: "u-1" } });
    await renderWithRouter(() => <PersonalMenu />);

    await signOutFromMenu();

    await waitFor(() =>
      expect(server.requests.some((request) => request.method === "POST")).toBe(true),
    );
    await waitFor(() => expect(recentContexts("checkout")).toEqual([]));
  });
});
