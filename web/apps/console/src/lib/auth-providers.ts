import {
  type AuthProvider,
  type AuthProviderCreateWritable,
  type AuthProviderPatchWritable,
  createAuthProvider,
  deleteAuthProvider,
  disableAuthProvider,
  enableAuthProvider,
  getAuthProvider,
  listAuthProviders,
  testAuthProvider,
  updateAuthProvider,
} from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";
import { queryOptions } from "@tanstack/react-query";
import { z } from "zod";

import { ApiError, unwrap } from "./errors";
import { ifMatch } from "./http";
import { violationMessage } from "./problem-messages";

/** Every query of the instance's auth providers, so a change invalidates by prefix. */
export const authProvidersKey = ["instance", "auth-providers"] as const;

/** The instance's auth providers, ordered by display name. */
export const authProvidersQuery = queryOptions<AuthProvider[]>({
  queryKey: authProvidersKey,
  queryFn: async () => (await unwrap(listAuthProviders())).items,
});

/** One auth provider by its slug. */
export function authProviderQuery(slug: string) {
  return queryOptions<AuthProvider>({
    queryKey: [...authProvidersKey, slug],
    queryFn: () => unwrap(getAuthProvider({ path: { provider: slug } })),
  });
}

/** The operations on auth providers, through the generated client. */
export const authProviderApi = {
  create: (body: AuthProviderCreateWritable) => unwrap(createAuthProvider({ body })),
  update: (provider: AuthProvider, body: AuthProviderPatchWritable) =>
    unwrap(
      updateAuthProvider({
        path: { provider: provider.slug },
        body,
        headers: ifMatch(provider.version),
      }),
    ),
  remove: (slug: string) => unwrap(deleteAuthProvider({ path: { provider: slug } })),
  enable: (slug: string) => unwrap(enableAuthProvider({ path: { provider: slug } })),
  disable: (slug: string) => unwrap(disableAuthProvider({ path: { provider: slug } })),
  test: (slug: string) => unwrap(testAuthProvider({ path: { provider: slug } })),
};

/** The scopes and claim names a provider is created with unless told otherwise. */
export const defaultScopes = ["openid", "email", "profile"] as const;
export const defaultClaims = { email: "email", name: "name", groups: "groups" } as const;

/** The slug that cannot name a provider: `/sso/complete/…` finishes a sign-in. */
const reservedSlug = "complete";
const slugPattern = /^[a-z][a-z0-9_-]{0,63}$/;
const domainPattern = /^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;

/** What a list typed into one field holds: its words, once each, in the order typed. */
export function wordsOf(text: string): string[] {
  return [...new Set(text.split(/[\s,]+/).filter((word) => word !== ""))];
}

/** A domain as the server stores it: lowercase, without a trailing dot. */
function domainOf(word: string): string {
  return word.toLowerCase().replace(/\.$/, "");
}

/** The domains a field holds, in the form the server stores them. */
function domainsOf(text: string): string[] {
  return [...new Set(wordsOf(text).map(domainOf))];
}

/** An OIDC provider as its form holds it; lists are the text a member types. */
export type OidcProviderForm = {
  slug: string;
  display_name: string;
  issuer: string;
  client_id: string;
  /** Write-only: empty keeps the stored secret. */
  client_secret: string;
  scopes: string;
  claim_email: string;
  claim_name: string;
  claim_groups: string;
  domains: string;
  link_by_email: boolean;
  auto_provision: boolean;
  require_verified_email: boolean;
  /** Only a new provider chooses; an existing one is enabled and disabled apart. */
  enabled: boolean;
};

/** A new provider's form, or an existing one's, never carrying its secret. */
export function formOf(provider?: AuthProvider): OidcProviderForm {
  return {
    slug: provider?.slug ?? "",
    display_name: provider?.display_name ?? "",
    issuer: provider?.issuer ?? "",
    client_id: provider?.client_id ?? "",
    client_secret: "",
    scopes: provider?.scopes?.join(" ") ?? defaultScopes.join(" "),
    claim_email: provider?.claims?.email ?? defaultClaims.email,
    claim_name: provider?.claims?.name ?? defaultClaims.name,
    claim_groups: provider?.claims?.groups ?? defaultClaims.groups,
    domains: provider?.domains.join(" ") ?? "",
    link_by_email: provider?.link_by_email ?? false,
    auto_provision: provider?.auto_provision ?? false,
    require_verified_email: provider?.require_verified_email ?? true,
    enabled: provider?.enabled ?? true,
  };
}

/** The request that creates the provider a form describes. */
export function bodyOf(values: OidcProviderForm): AuthProviderCreateWritable {
  return {
    slug: values.slug.trim(),
    kind: "oidc",
    display_name: values.display_name.trim(),
    enabled: values.enabled,
    domains: domainsOf(values.domains),
    link_by_email: values.link_by_email,
    auto_provision: values.auto_provision,
    issuer: values.issuer.trim(),
    client_id: values.client_id.trim(),
    client_secret: values.client_secret,
    scopes: wordsOf(values.scopes),
    claims: {
      email: values.claim_email.trim(),
      name: values.claim_name.trim(),
      groups: values.claim_groups.trim(),
    },
    require_verified_email: values.require_verified_email,
  };
}

function sameList(a: readonly string[], b: readonly string[]): boolean {
  return a.length === b.length && a.every((item, index) => item === b[index]);
}

/**
 * The request that changes provider into what a form describes: only the
 * properties that differ, so a save never overwrites what someone else
 * set, and a secret only when a new one was typed.
 */
export function patchOf(
  provider: AuthProvider,
  values: OidcProviderForm,
): AuthProviderPatchWritable {
  const patch: AuthProviderPatchWritable = {};
  const displayName = values.display_name.trim();
  if (displayName !== provider.display_name) patch.display_name = displayName;
  const domains = domainsOf(values.domains);
  if (!sameList(domains, provider.domains)) patch.domains = domains;
  if (values.link_by_email !== provider.link_by_email) patch.link_by_email = values.link_by_email;
  if (values.auto_provision !== provider.auto_provision) {
    patch.auto_provision = values.auto_provision;
  }
  const issuer = values.issuer.trim();
  if (issuer !== provider.issuer) patch.issuer = issuer;
  const clientId = values.client_id.trim();
  if (clientId !== provider.client_id) patch.client_id = clientId;
  if (values.client_secret !== "") patch.client_secret = values.client_secret;
  const scopes = wordsOf(values.scopes);
  if (!sameList(scopes, provider.scopes ?? [])) patch.scopes = scopes;
  const claims: NonNullable<AuthProviderPatchWritable["claims"]> = {};
  for (const [field, key] of [
    ["claim_email", "email"],
    ["claim_name", "name"],
    ["claim_groups", "groups"],
  ] as const) {
    const claim = values[field].trim();
    if (claim !== provider.claims?.[key]) claims[key] = claim;
  }
  if (Object.keys(claims).length > 0) patch.claims = claims;
  if (values.require_verified_email !== provider.require_verified_email) {
    patch.require_verified_email = values.require_verified_email;
  }
  return patch;
}

/** The JSON Pointer of each field of the form in the request body. */
export const pointerOf = {
  slug: "/slug",
  display_name: "/display_name",
  issuer: "/issuer",
  client_id: "/client_id",
  client_secret: "/client_secret",
  scopes: "/scopes",
  claim_email: "/claims/email",
  claim_name: "/claims/name",
  claim_groups: "/claims/groups",
  domains: "/domains",
} as const satisfies Partial<Record<keyof OidcProviderForm, string>>;

type PointedField = keyof typeof pointerOf;

/** Whether pointer is the field's or inside it, as `/domains/2` is inside `/domains`. */
function within(pointer: string, field: string): boolean {
  return pointer === field || pointer.startsWith(`${field}/`);
}

/** The sentence for the server's violation of a field of the form, if it found one. */
export function fieldViolation(
  i18n: I18n,
  refusal: unknown,
  field: PointedField,
): string | undefined {
  if (!(refusal instanceof ApiError)) return undefined;
  const violation = refusal.violations.find((v) => within(v.pointer, pointerOf[field]));
  return violation ? violationMessage(i18n, violation) : undefined;
}

/** Whether the refusal's violations all sit on fields of the form, which then say it themselves. */
export function fieldsExplain(refusal: unknown): boolean {
  if (!(refusal instanceof ApiError) || refusal.violations.length === 0) return false;
  const fields = Object.values(pointerOf);
  return refusal.violations.every((v) => fields.some((field) => within(v.pointer, field)));
}

/**
 * The form's checks before it asks the server, in the words the server's
 * violations are shown in. A provider that exists has its slug and keeps
 * its secret, so neither is asked for again.
 */
export function oidcFormSchema(i18n: I18n, { creating }: { creating: boolean }) {
  const violation = (pointer: string, code: string) =>
    violationMessage(i18n, { pointer, code, detail: "" });
  return z.object({
    slug: creating
      ? z
          .string()
          .trim()
          .min(1, i18n._(msg`Enter a slug.`))
          .superRefine((value, ctx) => {
            if (value === reservedSlug) {
              ctx.addIssue({ code: "custom", message: violation("/slug", "reserved_slug") });
            } else if (!slugPattern.test(value)) {
              ctx.addIssue({ code: "custom", message: violation("/slug", "invalid_slug") });
            }
          })
      : z.string(),
    display_name: z
      .string()
      .trim()
      .min(1, i18n._(msg`Enter a name.`))
      .max(200, violation("/display_name", "invalid_display_name")),
    issuer: z
      .string()
      .trim()
      .min(1, i18n._(msg`Enter the issuer's URL.`))
      .refine(
        (value) => {
          try {
            const url = new URL(value);
            return url.protocol === "https:" || url.protocol === "http:";
          } catch {
            return false;
          }
        },
        i18n._(msg`Enter an absolute URL, such as https://login.example.com.`),
      ),
    client_id: z
      .string()
      .trim()
      .min(1, i18n._(msg`Enter the client ID.`)),
    client_secret: creating
      ? z
          .string()
          .trim()
          .min(1, i18n._(msg`Enter the client secret.`))
      : z.string(),
    scopes: z.string(),
    claim_email: z
      .string()
      .trim()
      .min(1, i18n._(msg`Enter a claim name.`)),
    claim_name: z
      .string()
      .trim()
      .min(1, i18n._(msg`Enter a claim name.`)),
    claim_groups: z
      .string()
      .trim()
      .min(1, i18n._(msg`Enter a claim name.`)),
    domains: z.string().superRefine((value, ctx) => {
      const domains = domainsOf(value);
      if (domains.length === 0) {
        ctx.addIssue({ code: "custom", message: i18n._(msg`Enter at least one email domain.`) });
      } else if (domains.some((domain) => !domainPattern.test(domain))) {
        ctx.addIssue({ code: "custom", message: violation("/domains/0", "invalid_domain") });
      }
    }),
    link_by_email: z.boolean(),
    auto_provision: z.boolean(),
    require_verified_email: z.boolean(),
    enabled: z.boolean(),
  });
}

/** The address that starts a sign-in through a provider, keeping where the member was going. */
export function signInStartPath(slug: string, returnTo?: string): string {
  const start = `/sso/${encodeURIComponent(slug)}/start`;
  return returnTo ? `${start}?return_to=${encodeURIComponent(returnTo)}` : start;
}
