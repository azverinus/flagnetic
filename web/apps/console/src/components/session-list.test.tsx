import type { Session } from "@flagnetic/api";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeServer } from "@/test/fake-server";
import { renderWithProviders } from "@/test/render";

import { SessionList } from "./session-list";

const chrome =
  "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36";
const firefox = "Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0";

function session(id: string, userAgent: string, current: boolean): Session {
  return {
    id,
    method: "local",
    client_ip: "127.0.0.1",
    user_agent: userAgent,
    created_at: "2026-10-21T10:00:00Z",
    last_seen_at: "2026-10-21T11:00:00Z",
    expires_at: "2026-11-20T10:00:00Z",
    current,
  };
}

function rows() {
  return within(screen.getByRole("table", { name: "The devices you are signed in on" }))
    .getAllByRole("row")
    .slice(1);
}

describe("the session list", () => {
  it("lists this device first and marks it, with no way to revoke it here", async () => {
    fakeServer({
      "GET /me/sessions": {
        status: 200,
        body: { items: [session("s2", firefox, false), session("s1", chrome, true)] },
      },
    });
    renderWithProviders(<SessionList />);

    await waitFor(() => expect(rows()).toHaveLength(2));
    const [first, second] = rows();
    expect(first).toHaveTextContent("Chrome on macOS");
    expect(first).toHaveTextContent("This device");
    expect(within(first!).queryByRole("button")).not.toBeInTheDocument();
    expect(second).toHaveTextContent("Firefox on Linux");
    expect(second).not.toHaveTextContent("This device");
  });

  it("revokes another device after saying what happens to it", async () => {
    let items = [session("s1", chrome, true), session("s2", firefox, false)];
    const server = fakeServer({
      "GET /me/sessions": () => ({ status: 200, body: { items } }),
      "DELETE /me/sessions/s2": () => {
        items = items.slice(0, 1);
        return { status: 204 };
      },
    });
    renderWithProviders(<SessionList />);
    const user = userEvent.setup();

    await user.click(
      await screen.findByRole("button", { name: "Revoke the session on Firefox on Linux" }),
    );
    const dialog = await screen.findByRole("dialog", {
      name: "Revoke the session on Firefox on Linux",
    });
    await user.click(within(dialog).getByRole("button", { name: "Revoke session" }));

    await waitFor(() => expect(rows()).toHaveLength(1));
    expect(server.requests.some((r) => r.method === "DELETE")).toBe(true);
    expect(screen.getByText("You are signed in on this device only.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Revoke all other sessions" })).toBeDisabled();
  });

  it("revokes every other device at once, keeping this one", async () => {
    let items = [
      session("s1", chrome, true),
      session("s2", firefox, false),
      session("s3", firefox, false),
    ];
    const server = fakeServer({
      "GET /me/sessions": () => ({ status: 200, body: { items } }),
      "DELETE /me/sessions": () => {
        items = items.slice(0, 1);
        return { status: 200, body: { revoked: 2 } };
      },
    });
    renderWithProviders(<SessionList />);
    const user = userEvent.setup();

    await waitFor(() => expect(rows()).toHaveLength(3));
    await user.click(screen.getByRole("button", { name: "Revoke all other sessions" }));
    const dialog = await screen.findByRole("dialog", { name: "Revoke all other sessions" });
    await user.click(within(dialog).getByRole("button", { name: "Revoke 2 sessions" }));

    await waitFor(() => expect(rows()).toHaveLength(1));
    const revoke = server.requests.find((r) => r.method === "DELETE")!;
    expect(new URL(revoke.url).search).toBe("");
  });
});
