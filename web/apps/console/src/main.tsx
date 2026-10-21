import { i18n } from "@lingui/core";
import { I18nProvider } from "@lingui/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { createRouter, RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import "./styles.css";

import { activateLocale } from "./i18n/i18n";
import { instanceDefaultLocale, resolveLocale, storedLocale } from "./i18n/locale";
import { configureClient } from "./lib/http";
import { createQueryClient } from "./lib/query-client";
import { onSessionEnded } from "./lib/signed-in";
import { applyTheme, readTheme } from "./lib/theme";
import { routeTree } from "./routeTree.gen";

const queryClient = createQueryClient(i18n);

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
