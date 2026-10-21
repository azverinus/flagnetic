import { i18n } from "@lingui/core";
import { I18nProvider } from "@lingui/react";
import { MutationCache, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRouter, RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import "./styles.css";

import { activateLocale } from "./i18n/i18n";
import { instanceDefaultLocale, resolveLocale, storedLocale } from "./i18n/locale";
import { ApiError } from "./lib/errors";
import { configureClient } from "./lib/http";
import { onSessionEnded } from "./lib/signed-in";
import { applyTheme, readTheme } from "./lib/theme";
import { toastError } from "./lib/toast";
import { routeTree } from "./routeTree.gen";

/** A refusal is the server's answer; only a failure to reach it is worth retrying. */
function retry(count: number, error: unknown): boolean {
  return count < 2 && error instanceof ApiError && (error.status === 0 || error.status >= 500);
}

const queryClient = new QueryClient({
  defaultOptions: { queries: { retry } },
  mutationCache: new MutationCache({
    onError: (error, _variables, _context, mutation) => {
      // An ended session goes back to sign-in instead of toasting.
      if (mutation.meta?.toast === false || (error instanceof ApiError && error.status === 401)) {
        return;
      }
      toastError(i18n, error);
    },
  }),
});

const router = createRouter({
  routeTree,
  context: { queryClient },
  defaultPreload: "intent",
  scrollRestoration: true,
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

configureClient(
  onSessionEnded(
    queryClient,
    () => router.state.location,
    (search) => void router.navigate({ to: "/sign-in", search }),
  ),
);

async function start() {
  applyTheme(readTheme());
  await activateLocale(
    resolveLocale({
      stored: storedLocale(),
      browser: navigator.languages,
      instanceDefault: instanceDefaultLocale(),
    }),
  );
  createRoot(document.getElementById("root")!).render(
    <StrictMode>
      <I18nProvider i18n={i18n}>
        <QueryClientProvider client={queryClient}>
          <RouterProvider router={router} />
        </QueryClientProvider>
      </I18nProvider>
    </StrictMode>,
  );
}

void start();
