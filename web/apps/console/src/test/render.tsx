import { i18n, type Messages } from "@lingui/core";
import { I18nProvider } from "@lingui/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, type RenderResult } from "@testing-library/react";
import type { ReactNode } from "react";

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
