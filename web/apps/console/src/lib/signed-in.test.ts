import { QueryClient } from "@tanstack/react-query";
import { isRedirect } from "@tanstack/react-router";
import { describe, expect, it, vi } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";

import { meQuery } from "./queries";
import { onSessionEnded, requireSignedIn, signInSearch, validateSignInSearch } from "./signed-in";

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

  it("does not send a member back to the sign-in page itself", () => {
    expect(signInSearch({ pathname: "/sign-in", searchStr: "?ended=true" }, true)).toEqual({
      ended: true,
    });
    expect(signInSearch({ pathname: "/sign-ins" }, false)).toEqual({ returnTo: "/sign-ins" });
  });
});
