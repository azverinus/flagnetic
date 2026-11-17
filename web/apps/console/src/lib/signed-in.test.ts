import { QueryClient } from "@tanstack/react-query";
import { isRedirect } from "@tanstack/react-router";
import { afterEach, describe, expect, it, vi } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";

import { meQuery } from "./queries";
import {
  announceSessionChange,
  listenForSessionChange,
  onSessionChangedElsewhere,
  onSessionEnded,
  requireSignedIn,
  signInSearch,
  validateLinkErrorSearch,
  validateSignInSearch,
} from "./signed-in";

const me = {
  member: {
    id: "0192f0c4-0000-7000-8000-000000000001",
    email: "ada@example.com",
    display_name: "Ada",
    status: "active",
    instance_admin: false,
    locale: null,
  },
  credential: { method: "session" },
  client_ip: "127.0.0.1",
};

async function redirectOf(promise: Promise<unknown>) {
  try {
    await promise;
  } catch (error) {
    if (isRedirect(error)) return error.options;
    throw error;
  }
  throw new Error("no redirect");
}

describe("the guard on signed-in routes", () => {
  it("lets a signed-in member through", async () => {
    fakeServer({ "GET /me": { status: 200, body: me } });

    await expect(
      requireSignedIn(new QueryClient(), { pathname: "/projects/checkout" }),
    ).resolves.toMatchObject({ member: { email: "ada@example.com" } });
  });

  it("sends a visitor to sign-in keeping where they were", async () => {
    fakeServer({ "GET /me": problem(401, "unauthenticated") });

    const to = await redirectOf(
      requireSignedIn(new QueryClient(), { pathname: "/projects/checkout", searchStr: "?tab=x" }),
    );

    expect(to).toMatchObject({ to: "/sign-in", search: { returnTo: "/projects/checkout?tab=x" } });
    expect((to.search as { ended?: boolean }).ended).toBeUndefined();
  });

  it("says the session ended when it expired or was revoked", async () => {
    fakeServer({ "GET /me": problem(401, "session_expired") });

    const to = await redirectOf(requireSignedIn(new QueryClient(), { pathname: "/" }));

    expect(to).toMatchObject({ to: "/sign-in", search: { ended: true } });
  });
});

describe("a request refused as not signed in", () => {
  it("sends a member who was signed in back to sign-in, keeping where they were", () => {
    const queryClient = new QueryClient();
    queryClient.setQueryData(meQuery.queryKey, me as never);
    const navigate = vi.fn();

    onSessionEnded(queryClient, () => ({ pathname: "/projects/checkout" }), navigate)();

    expect(navigate).toHaveBeenCalledWith({ returnTo: "/projects/checkout", ended: true });
    expect(queryClient.getQueryData(meQuery.queryKey)).toBeUndefined();
  });

  it("does not navigate again once the console is on its way to sign-in", () => {
    // A stale caller refetched while the guard already redirects to
    // sign-in: that redirect carries where the member was, and a second
    // navigation would only replace it.
    const queryClient = new QueryClient();
    queryClient.setQueryData(meQuery.queryKey, me as never, { updatedAt: 0 });
    const navigate = vi.fn();

    onSessionEnded(
      queryClient,
      () => ({ pathname: "/sign-in", searchStr: "?ended=true" }),
      navigate,
    )();

    expect(navigate).not.toHaveBeenCalled();
    expect(queryClient.getQueryData(meQuery.queryKey)).toBeUndefined();
  });

  it("keeps quiet for a visitor who never signed in", () => {
    const navigate = vi.fn();

    onSessionEnded(new QueryClient(), () => ({ pathname: "/" }), navigate)();

    expect(navigate).not.toHaveBeenCalled();
  });
});

describe("the sign-in page's search", () => {
  it("never carries a return path to another origin", () => {
    expect(validateSignInSearch({ returnTo: "//evil.example" })).toEqual({});
    expect(validateSignInSearch({ returnTo: "https://evil.example" })).toEqual({});
    expect(validateSignInSearch({ returnTo: "/projects/checkout", ended: "true" })).toEqual({
      returnTo: "/projects/checkout",
      ended: true,
    });
  });

  it("keeps the error a failed single sign-on sent, and the provider it names, only as codes", () => {
    expect(validateSignInSearch({ error: "sso_required", provider: "acme" })).toEqual({
      error: "sso_required",
      provider: "acme",
    });
    expect(validateSignInSearch({ error: "<script>", provider: "acme" })).toEqual({});
    expect(validateSignInSearch({ error: "sso_required", provider: "A b" })).toEqual({
      error: "sso_required",
    });
    expect(validateSignInSearch({ provider: "acme" })).toEqual({});
  });

  it("does not send a member back to the sign-in page itself", () => {
    expect(signInSearch({ pathname: "/sign-in", searchStr: "?ended=true" }, true)).toEqual({
      ended: true,
    });
    expect(signInSearch({ pathname: "/sign-ins" }, false)).toEqual({ returnTo: "/sign-ins" });
  });
});

describe("the search of the page a failed linking returns to", () => {
  it("keeps the error only as a code", () => {
    expect(validateLinkErrorSearch({ error: "identity_linked" })).toEqual({
      error: "identity_linked",
    });
    expect(validateLinkErrorSearch({ error: "<script>" })).toEqual({});
    expect(validateLinkErrorSearch({ error: 3 })).toEqual({});
    expect(validateLinkErrorSearch({})).toEqual({});
  });
});

describe("a session change in another tab", () => {
  const where = () => ({ pathname: "/projects/checkout" });

  it("sends a tab that holds a member to sign-in once they signed out elsewhere", () => {
    const queryClient = new QueryClient();
    queryClient.setQueryData(meQuery.queryKey, me as never);
    const navigate = vi.fn();

    onSessionChangedElsewhere(queryClient, where, navigate, vi.fn())("signed-out");

    expect(navigate).toHaveBeenCalledWith({ returnTo: "/projects/checkout", ended: true });
    expect(queryClient.getQueryData(meQuery.queryKey)).toBeUndefined();
  });

  it("drops what a tab holds of the first member when another signs in, and reads it again", () => {
    const queryClient = new QueryClient();
    queryClient.setQueryData(meQuery.queryKey, me as never);
    const navigate = vi.fn();
    const reload = vi.fn();

    onSessionChangedElsewhere(queryClient, where, navigate, reload)("signed-in");

    expect(queryClient.getQueryData(meQuery.queryKey)).toBeUndefined();
    expect(reload).toHaveBeenCalledOnce();
    expect(navigate).not.toHaveBeenCalled();
  });

  it("leaves a tab that holds nobody alone", () => {
    const navigate = vi.fn();
    const reload = vi.fn();
    const change = onSessionChangedElsewhere(new QueryClient(), where, navigate, reload);

    change("signed-in");
    change("signed-out");

    expect(navigate).not.toHaveBeenCalled();
    expect(reload).not.toHaveBeenCalled();
  });
});

describe("telling the other tabs", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("reaches a listener in another tab, and only for a change it knows", () => {
    const open = new Set<{ onmessage: ((event: MessageEvent) => void) | null }>();
    class FakeChannel {
      onmessage: ((event: MessageEvent) => void) | null = null;
      constructor() {
        open.add(this);
      }
      postMessage(data: unknown) {
        for (const other of open) {
          if (other !== this) other.onmessage?.({ data } as MessageEvent);
        }
      }
      close() {
        open.delete(this);
      }
    }
    vi.stubGlobal("BroadcastChannel", FakeChannel);
    const heard = vi.fn();
    const stop = listenForSessionChange(heard);

    announceSessionChange("signed-out");
    for (const channel of open) channel.onmessage?.({ data: "something else" } as MessageEvent);

    expect(heard).toHaveBeenCalledOnce();
    expect(heard).toHaveBeenCalledWith("signed-out");
    stop();
    expect(open.size).toBe(0);
  });

  it("does nothing where the browser has no broadcast channel", () => {
    vi.stubGlobal("BroadcastChannel", undefined);

    expect(() => announceSessionChange("signed-out")).not.toThrow();
    expect(() => listenForSessionChange(vi.fn())()).not.toThrow();
  });
});
