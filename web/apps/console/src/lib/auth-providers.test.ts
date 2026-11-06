import type { AuthProvider } from "@flagnetic/api";
import { i18n } from "@lingui/core";
import { beforeEach, describe, expect, it } from "vitest";

import { activateForTests } from "@/test/render";

import {
  bodyOf,
  formOf,
  type OidcProviderForm,
  oidcFormSchema,
  patchOf,
  signInStartPath,
  wordsOf,
} from "./auth-providers";

const provider: AuthProvider = {
  id: "0192f0c4-0000-7000-8000-000000000010",
  slug: "acme",
  kind: "oidc",
  display_name: "Acme",
  enabled: true,
  domains: ["acme.example"],
  link_by_email: false,
  auto_provision: false,
  issuer: "https://login.acme.example",
  client_id: "flagnetic",
  client_secret_set: true,
  scopes: ["openid", "email", "profile"],
  claims: { email: "email", name: "name", groups: "groups" },
  require_verified_email: true,
  redirect_uri: "https://flags.example/sso/acme/callback",
  created_at: "2026-11-01T00:00:00Z",
  updated_at: "2026-11-01T00:00:00Z",
  version: 3,
};

beforeEach(() => activateForTests("en"));

describe("the words of a list a member types", () => {
  it.each([
    ["a b  c", ["a", "b", "c"]],
    ["a, b,c\nd", ["a", "b", "c", "d"]],
    ["  ", []],
    ["a a", ["a"]],
  ])("reads %j as %j", (text, words) => {
    expect(wordsOf(text)).toEqual(words);
  });
});

describe("the form of a provider", () => {
  it("holds what the provider answers, and never a secret", () => {
    expect(formOf(provider)).toEqual({
      slug: "acme",
      display_name: "Acme",
      issuer: "https://login.acme.example",
      client_id: "flagnetic",
      client_secret: "",
      scopes: "openid email profile",
      claim_email: "email",
      claim_name: "name",
      claim_groups: "groups",
      domains: "acme.example",
      link_by_email: false,
      auto_provision: false,
      require_verified_email: true,
      enabled: true,
    });
  });
});

describe("what a form sends", () => {
  const filled: OidcProviderForm = {
    ...formOf(provider),
    slug: "new-one",
    client_secret: "s3cret",
    domains: "Acme.Example, other.example",
    link_by_email: true,
  };

  it("creates with every field the form holds, the domains lowercased", () => {
    expect(bodyOf(filled)).toEqual({
      slug: "new-one",
      kind: "oidc",
      display_name: "Acme",
      enabled: true,
      domains: ["acme.example", "other.example"],
      link_by_email: true,
      auto_provision: false,
      issuer: "https://login.acme.example",
      client_id: "flagnetic",
      client_secret: "s3cret",
      scopes: ["openid", "email", "profile"],
      claims: { email: "email", name: "name", groups: "groups" },
      require_verified_email: true,
    });
  });

  it("edits with only what changed, and no secret unless a new one was typed", () => {
    expect(patchOf(provider, formOf(provider))).toEqual({});
    expect(
      patchOf(provider, {
        ...formOf(provider),
        display_name: "Acme SSO",
        domains: "acme.example other.example",
        claim_groups: "roles",
        auto_provision: true,
      }),
    ).toEqual({
      display_name: "Acme SSO",
      domains: ["acme.example", "other.example"],
      claims: { groups: "roles" },
      auto_provision: true,
    });
    expect(patchOf(provider, { ...formOf(provider), client_secret: "rotated" })).toEqual({
      client_secret: "rotated",
    });
  });

  it("sends a changed order of scopes, but not changed spacing", () => {
    expect(patchOf(provider, { ...formOf(provider), scopes: "profile email openid" })).toEqual({
      scopes: ["profile", "email", "openid"],
    });
    expect(patchOf(provider, { ...formOf(provider), scopes: "openid  email,profile" })).toEqual({});
  });
});

describe("the checks of the form before it asks the server", () => {
  const check = (values: Partial<OidcProviderForm>, creating = true) =>
    oidcFormSchema(i18n, { creating }).safeParse({
      ...formOf(provider),
      client_secret: "x",
      ...values,
    });
  const messages = (values: Partial<OidcProviderForm>, creating = true) => {
    const result = check(values, creating);
    return result.success ? [] : result.error.issues.map((issue) => issue.message);
  };

  it("accepts a complete provider", () => {
    expect(check({ slug: "acme-2" }).success).toBe(true);
  });

  it("asks for a slug the server accepts, and not the reserved one", () => {
    expect(messages({ slug: "" })).toContain("Enter a slug.");
    expect(messages({ slug: "Acme" })).toContain(
      "Start with a lowercase letter; then lowercase letters, digits, - and _, at most 64 in all.",
    );
    expect(messages({ slug: "complete" })).toContain("This slug is reserved.");
  });

  it("does not ask for the slug of a provider that has one", () => {
    expect(check({ slug: "" }, false).success).toBe(true);
  });

  it("asks for the secret only when creating", () => {
    expect(messages({ client_secret: "" })).toContain("Enter the client secret.");
    expect(check({ client_secret: "" }, false).success).toBe(true);
  });

  it("asks for an issuer that is a URL", () => {
    expect(messages({ issuer: "" })).toContain("Enter the issuer's URL.");
    expect(messages({ issuer: "login.acme.example" })).toContain(
      "Enter an absolute URL, such as https://login.example.com.",
    );
    expect(check({ issuer: "http://localhost:5556/dex" }).success).toBe(true);
  });

  it("asks for at least one domain, each a host name", () => {
    expect(messages({ domains: " " })).toContain("Enter at least one email domain.");
    expect(messages({ domains: "acme.example @acme.example" })).toContain(
      "Enter host names such as example.com, without an at sign or a scheme.",
    );
  });

  it("asks for a client ID, a name and claim names", () => {
    expect(messages({ client_id: "" })).toContain("Enter the client ID.");
    expect(messages({ display_name: " " })).toContain("Enter a name.");
    expect(messages({ claim_email: "" })).toContain("Enter a claim name.");
  });
});

describe("the sign-in address of a provider", () => {
  it("names the provider and keeps where the member was going", () => {
    expect(signInStartPath("acme")).toBe("/sso/acme/start");
    expect(signInStartPath("acme", "/projects/checkout?tab=a")).toBe(
      "/sso/acme/start?return_to=%2Fprojects%2Fcheckout%3Ftab%3Da",
    );
  });
});
