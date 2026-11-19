import { i18n, type Messages } from "@lingui/core";
import { I18nProvider } from "@lingui/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  type AnyRouter,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from "@tanstack/react-router";
import { render, type RenderResult } from "@testing-library/react";
import type { Me } from "@flagnetic/api";
import type { ReactNode } from "react";

import { meQuery } from "@/lib/queries";

import { messages as en } from "@/locales/en/messages.po";
import { messages as pseudo } from "@/locales/pseudo/messages.po";
import { messages as ru } from "@/locales/ru/messages.po";

const catalogs: Record<string, Messages> = { en, ru, pseudo };

/** Activates a locale's catalog synchronously, as a test needs. */
export function activateForTests(locale: "en" | "ru" | "pseudo"): void {
  i18n.loadAndActivate({ locale, messages: catalogs[locale]! });
}

/** A query client for one test: no retries, nothing cached between tests. */
export function testQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
}

/**
 * A query client for a test of a screen that knows who is signed in: the
 * caller is the member with the given id, as the console's own guard has
 * already answered before it renders anything.
 */
export function signedInClient(member = "ada"): QueryClient {
  const client = testQueryClient();
  client.setQueryData(meQuery.queryKey, {
    client_ip: "127.0.0.1",
    credential: { method: "session" },
    member: {
      id: member,
      email: `${member}@example.com`,
      display_name: member,
      status: "active",
      instance_admin: false,
      locale: null,
    },
    max_token_lifetime_seconds: null,
    require_token_expiry: false,
  } satisfies Me);
  return client;
}

/** Renders ui with the catalogs and a fresh query client. */
export function renderWithProviders(
  ui: ReactNode,
  queryClient: QueryClient = testQueryClient(),
): RenderResult & { queryClient: QueryClient } {
  const result = render(
    <I18nProvider i18n={i18n}>
      <QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>
    </I18nProvider>,
  );
  return { ...result, queryClient };
}

/**
 * Renders ui as the page at path of a router on memory history, with a
 * second page at /elsewhere to navigate to, as a primitive that reads the
 * address or blocks leaving it needs.
 */
export async function renderWithRouter(
  ui: () => ReactNode,
  path = "/",
  queryClient: QueryClient = testQueryClient(),
): Promise<RenderResult & { router: AnyRouter; queryClient: QueryClient }> {
  const root = createRootRoute({ component: Outlet });
  const page = createRoute({ getParentRoute: () => root, path: "/", component: () => <>{ui()}</> });
  const elsewhere = createRoute({
    getParentRoute: () => root,
    path: "/elsewhere",
    component: () => <p>Elsewhere</p>,
  });
  const router = createRouter({
    routeTree: root.addChildren([page, elsewhere]),
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  await router.load();
  const result = renderWithProviders(<RouterProvider router={router} />, queryClient);
  return { ...result, router };
}
