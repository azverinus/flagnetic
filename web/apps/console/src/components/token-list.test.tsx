import type { ApiToken } from "@flagnetic/api";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";

import type { TokenPolicy } from "@/lib/personal-tokens";
import { fakeServer, problem } from "@/test/fake-server";
import { renderWithProviders } from "@/test/render";

import { TokenList } from "./token-list";

const value = `fn_pat_${"A".repeat(40)}`;
const token: ApiToken = {
  id: "t1",
  name: "reporting script",
  hint: "fn_pat_AAAA",
  scopes: ["read"],
  expires_at: null,
  created_at: "2026-10-21T10:00:00Z",
  last_used_at: null,
  last_used_ip: null,
  revoked_at: null,
};

/** A fresh instance's policy: a year at most, and a token may never expire. */
const yearAhead: TokenPolicy = { maxLifetimeSeconds: 365 * 86_400, expiryRequired: false };

async function openCreate() {
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "New API token" }));
  return { user, dialog: await screen.findByRole("dialog", { name: "New API token" }) };
}

describe("the personal access tokens", () => {
  it("say when there is none yet", async () => {
    fakeServer({ "GET /me/tokens": { status: 200, body: { items: [] } } });
    renderWithProviders(<TokenList instanceAdmin={false} policy={yearAhead} />);
    expect(await screen.findByText("You have no API token yet.")).toBeInTheDocument();
  });

  it("create a read token expiring in thirty days by default, then show its value once", async () => {
    let items: ApiToken[] = [];
    const server = fakeServer({
      "GET /me/tokens": () => ({ status: 200, body: { items } }),
      "POST /me/tokens": () => {
        items = [token];
        return { status: 201, body: { token, value } };
      },
    });
    renderWithProviders(<TokenList instanceAdmin={false} policy={yearAhead} />);
    const { user, dialog } = await openCreate();

    expect(within(dialog).getByRole("checkbox", { name: /^read/ })).toBeChecked();
    expect(within(dialog).queryByRole("checkbox", { name: /instance:admin/ })).toBeNull();
    expect(within(dialog).getByRole("combobox", { name: "Expires" })).toHaveTextContent("30 days");
    await user.type(within(dialog).getByLabelText("Name"), "reporting script");
    const before = Date.now();
    await user.click(within(dialog).getByRole("button", { name: "Create token" }));

    const reveal = await screen.findByRole("dialog", { name: "Your new API token" });
    expect(within(reveal).getByText(value)).toBeInTheDocument();
    const sent = (await server.requests.find((r) => r.method === "POST")!.json()) as {
      name: string;
      scopes: string[];
      expires_at: string;
    };
    expect(sent.name).toBe("reporting script");
    expect(sent.scopes).toEqual(["read"]);
    const ahead = new Date(sent.expires_at).getTime() - before;
    expect(Math.round(ahead / 86_400_000)).toBe(30);

    expect(within(reveal).getByRole("button", { name: "Close" })).toBeDisabled();
    await user.click(within(reveal).getByLabelText("I have stored it somewhere safe"));
    await user.click(within(reveal).getByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.queryByText(value)).not.toBeInTheDocument());
    expect(await screen.findByRole("row", { name: /reporting script/ })).toHaveTextContent(
      "Active",
    );
  });

  it("include read with any write scope, and send a token that never expires as null", async () => {
    const server = fakeServer({
      "GET /me/tokens": { status: 200, body: { items: [] } },
      "POST /me/tokens": { status: 201, body: { token, value } },
    });
    renderWithProviders(<TokenList instanceAdmin={false} policy={yearAhead} />);
    const { user, dialog } = await openCreate();

    await user.type(within(dialog).getByLabelText("Name"), "deploy");
    await user.click(within(dialog).getByRole("checkbox", { name: /^flags:write/ }));
    const read = within(dialog).getByRole("checkbox", { name: /^read/ });
    expect(read).toBeChecked();
    expect(read).toBeDisabled();
    await user.click(within(dialog).getByRole("combobox", { name: "Expires" }));
    await user.click(await screen.findByRole("option", { name: "Never" }));
    await user.click(within(dialog).getByRole("button", { name: "Create token" }));

    await screen.findByRole("dialog", { name: "Your new API token" });
    expect(await server.requests.find((r) => r.method === "POST")!.json()).toEqual({
      name: "deploy",
      scopes: ["read", "flags:write"],
      expires_at: null,
    });
  });

  it("offer only the expiries the instance allows, to a member who is no instance admin", async () => {
    const server = fakeServer({ "GET /me/tokens": { status: 200, body: { items: [] } } });
    renderWithProviders(
      <TokenList
        instanceAdmin={false}
        policy={{ maxLifetimeSeconds: 60 * 86_400, expiryRequired: true }}
      />,
    );
    const { user, dialog } = await openCreate();

    expect(
      within(dialog).getByText(
        "This instance allows a token at most 60 days ahead, and every token must expire.",
      ),
    ).toBeInTheDocument();
    expect(within(dialog).getByRole("combobox", { name: "Expires" })).toHaveTextContent("30 days");
    await user.click(within(dialog).getByRole("combobox", { name: "Expires" }));
    const options = (await screen.findAllByRole("option")).map((o) => o.textContent);
    expect(options).toEqual(["7 days", "30 days"]);
    expect(server.requests.some((r) => r.url.includes("auth-settings"))).toBe(false);
  });

  it("move a chosen expiry the instance stops allowing while the form is open to one it allows", async () => {
    fakeServer({ "GET /me/tokens": { status: 200, body: { items: [] } } });
    let bound: (policy: TokenPolicy) => void = () => undefined;
    function Page() {
      const [policy, setPolicy] = useState(yearAhead);
      bound = setPolicy;
      return <TokenList instanceAdmin={false} policy={policy} />;
    }
    renderWithProviders(<Page />);
    const { user, dialog } = await openCreate();
    await user.click(within(dialog).getByRole("combobox", { name: "Expires" }));
    await user.click(await screen.findByRole("option", { name: "Never" }));

    // As when the caller is read again after an instance admin's change.
    act(() => bound({ maxLifetimeSeconds: 10 * 86_400, expiryRequired: true }));
    await waitFor(() =>
      expect(within(dialog).getByRole("combobox", { name: "Expires" })).toHaveTextContent("7 days"),
    );
  });

  it("offer instance:admin to an instance admin", async () => {
    fakeServer({ "GET /me/tokens": { status: 200, body: { items: [] } } });
    renderWithProviders(<TokenList instanceAdmin policy={yearAhead} />);
    const { dialog } = await openCreate();
    expect(within(dialog).getByRole("checkbox", { name: /instance:admin/ })).toBeInTheDocument();
  });

  it("show a token that expires while the page is open as expired", async () => {
    // The clock stands still until the test moves it past the expiry, so a
    // slow render cannot let the token expire before it is first shown.
    // Only Date is faked: the page's timers and the queries' polling stay
    // real. The page's timer first fires while the clock still stands before
    // the expiry, as a timer firing early would, and must schedule itself
    // again to find the expiry passed once the clock moves.
    vi.useFakeTimers({ toFake: ["Date"] });
    try {
      const opened = Date.now();
      const soon = new Date(opened + 50).toISOString();
      fakeServer({
        "GET /me/tokens": { status: 200, body: { items: [{ ...token, expires_at: soon }] } },
      });
      renderWithProviders(<TokenList instanceAdmin={false} policy={yearAhead} />);

      const row = await screen.findByRole("row", { name: /reporting script/ });
      expect(row).toHaveTextContent("Active");
      await act(() => new Promise((resolve) => setTimeout(resolve, 200)));
      expect(row).toHaveTextContent("Active");
      vi.setSystemTime(opened + 1_000);
      await waitFor(() => expect(row).toHaveTextContent("Expired"), { timeout: 3_000 });
    } finally {
      vi.useRealTimers();
    }
  });

  it("explain an expiry beyond the instance's bound under its field", async () => {
    fakeServer({
      "GET /me/tokens": { status: 200, body: { items: [] } },
      "POST /me/tokens": problem(422, "invalid_token_request", {
        errors: [
          {
            pointer: "/expires_at",
            code: "beyond_max_lifetime",
            detail: "",
            params: { max_lifetime_seconds: 7 * 86_400 },
          },
        ],
      }),
    });
    renderWithProviders(<TokenList instanceAdmin={false} policy={yearAhead} />);
    const { user, dialog } = await openCreate();

    await user.type(within(dialog).getByLabelText("Name"), "deploy");
    await user.click(within(dialog).getByRole("button", { name: "Create token" }));

    expect(
      await within(dialog).findByText("This instance allows a token at most 7 days ahead."),
    ).toBeInTheDocument();
    expect(within(dialog).getByRole("combobox", { name: "Expires" })).toHaveAttribute(
      "aria-invalid",
      "true",
    );
  });

  it("revoke a token, which then shows as revoked with nothing left to do", async () => {
    let items = [token];
    const server = fakeServer({
      "GET /me/tokens": () => ({ status: 200, body: { items } }),
      "DELETE /me/tokens/t1": () => {
        items = [{ ...token, revoked_at: "2026-10-21T12:00:00Z" }];
        return { status: 204 };
      },
    });
    renderWithProviders(<TokenList instanceAdmin={false} policy={yearAhead} />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Revoke reporting script" }));
    const dialog = await screen.findByRole("dialog", { name: "Revoke reporting script" });
    await user.click(within(dialog).getByRole("button", { name: "Revoke token" }));

    const row = await screen.findByRole("row", { name: /Revoked/ });
    expect(within(row).queryByRole("button")).not.toBeInTheDocument();
    expect(server.requests.some((r) => r.method === "DELETE")).toBe(true);
  });
});
