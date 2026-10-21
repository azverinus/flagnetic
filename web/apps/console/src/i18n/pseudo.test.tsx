import { waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AuthSettingsForm } from "@/components/auth-settings-form";
import { InvitationAcceptance } from "@/components/invitation-acceptance";
import { PasswordForm } from "@/components/password-form";
import { ProfileForm } from "@/components/profile-form";
import { ProjectTokenList } from "@/components/project-token-list";
import { SessionList } from "@/components/session-list";
import { SignInForm } from "@/components/sign-in-form";
import { TokenList } from "@/components/token-list";
import { languageName } from "@/i18n/format";
import { locales } from "@/i18n/locale";
import { fakeServer, type FakeAnswer } from "@/test/fake-server";
import { activateForTests, renderWithRouter } from "@/test/render";

/**
 * Every text node and every accessible name of what renders, which in the
 * pseudo-locale must all come from a catalog.
 */
function visibleStrings(root: HTMLElement): string[] {
  const out: string[] = [];
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const text = node.textContent?.trim();
    if (text) out.push(text);
  }
  for (const element of root.querySelectorAll("[aria-label], [title], [placeholder]")) {
    for (const name of ["aria-label", "title", "placeholder"]) {
      const value = element.getAttribute(name)?.trim();
      if (value) out.push(value);
    }
  }
  return out;
}

/** A pseudo-localized message: wrapped in the markers the catalog adds. */
const localized = /^⟦[\s\S]*⟧$/;

/**
 * Each language the console offers, named in that language rather than in
 * the reader's, so a member finds their own whatever the console shows.
 */
const languageNames = new Set(locales.map(languageName));

const policy = { maxLifetimeSeconds: 365 * 86_400, expiryRequired: false };
const none: FakeAnswer = { status: 200, body: { items: [] } };

/**
 * The screens' own components, each with what it needs to render without
 * data of its own to show, so every string left is the console's.
 */
const screens: { name: string; routes?: Record<string, FakeAnswer>; ui: () => ReactNode }[] = [
  { name: "the sign-in form", ui: () => <SignInForm ended onSignedIn={vi.fn()} /> },
  {
    name: "the invitation acceptance",
    routes: { "GET /auth/invitations/s3cret": { status: 404 } },
    ui: () => <InvitationAcceptance secret="s3cret" onAccepted={vi.fn()} />,
  },
  { name: "the password form", ui: () => <PasswordForm /> },
  {
    name: "the profile form",
    ui: () => (
      <ProfileForm
        member={{
          id: "m1",
          email: "",
          display_name: "",
          status: "active",
          instance_admin: false,
          locale: null,
        }}
      />
    ),
  },
  { name: "the session list", routes: { "GET /me/sessions": none }, ui: () => <SessionList /> },
  {
    name: "the personal access tokens",
    routes: { "GET /me/tokens": none },
    ui: () => <TokenList instanceAdmin={false} policy={policy} />,
  },
  {
    name: "the project tokens",
    routes: { "GET /projects/checkout/tokens": none },
    ui: () => <ProjectTokenList project="checkout" roles={[]} policy={policy} names={new Map()} />,
  },
  {
    name: "the auth settings form",
    ui: () => (
      <AuthSettingsForm
        settings={{
          session_idle_timeout_seconds: 86_400,
          session_absolute_timeout_seconds: 2_592_000,
          token_max_lifetime_seconds: 31_536_000,
          token_expiry_required: false,
          password_min_length: 12,
        }}
      />
    ),
  },
];

afterEach(() => activateForTests("en"));

describe("the pseudo-locale", () => {
  it.each(screens)("renders every string of $name from the catalog", async ({ routes, ui }) => {
    fakeServer(routes ?? {});
    activateForTests("pseudo");
    const { container, queryClient } = await renderWithRouter(ui);
    await waitFor(() => expect(queryClient.isFetching()).toBe(0));

    const strings = visibleStrings(container);

    expect(strings.length).toBeGreaterThan(1);
    expect(strings.filter((s) => !localized.test(s) && !languageNames.has(s))).toEqual([]);
  });
});
