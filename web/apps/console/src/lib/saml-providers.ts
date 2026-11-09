import type {
  AuthProvider,
  AuthProviderCreateWritable,
  AuthProviderPatchWritable,
  SamlNameIdFormat,
} from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";
import { z } from "zod";

import {
  defaultClaims,
  domainsOf,
  httpURLField,
  providerFields,
  sameList,
  within,
} from "./auth-providers";
import { ApiError } from "./errors";
import { violationMessage } from "./problem-messages";

/** The name identifier formats a SAML provider may ask for, the default first. */
export const nameIDFormats = [
  "unspecified",
  "emailAddress",
  "persistent",
  "transient",
] as const satisfies readonly SamlNameIdFormat[];

/** Where the identity provider's metadata comes from: fetched from a URL, or pasted. */
export type MetadataSource = "url" | "document";

/** A SAML provider as its form holds it; lists are the text a member types. */
export type SamlProviderForm = {
  slug: string;
  display_name: string;
  metadata_source: MetadataSource;
  idp_metadata_url: string;
  /** Write-only: the server never answers a pasted document, so it starts empty. */
  idp_metadata_xml: string;
  sign_requests: boolean;
  name_id_format: SamlNameIdFormat;
  claim_email: string;
  claim_name: string;
  claim_groups: string;
  domains: string;
  link_by_email: boolean;
  auto_provision: boolean;
  /** Only a new provider chooses; an existing one is enabled and disabled apart. */
  enabled: boolean;
};

/**
 * Whether a stored provider's metadata was pasted: the server answers the
 * URL it fetches from and never the document, so a provider without a URL
 * holds a document.
 */
export function pastedMetadata(provider: AuthProvider): boolean {
  return provider.idp_metadata_url === undefined;
}

/** A new SAML provider's form, or an existing one's, never carrying a pasted document. */
export function samlFormOf(provider?: AuthProvider): SamlProviderForm {
  return {
    slug: provider?.slug ?? "",
    display_name: provider?.display_name ?? "",
    metadata_source: provider && pastedMetadata(provider) ? "document" : "url",
    idp_metadata_url: provider?.idp_metadata_url ?? "",
    idp_metadata_xml: "",
    sign_requests: provider?.sign_requests ?? true,
    name_id_format: provider?.name_id_format ?? "unspecified",
    claim_email: provider?.claims?.email ?? defaultClaims.email,
    claim_name: provider?.claims?.name ?? defaultClaims.name,
    claim_groups: provider?.claims?.groups ?? defaultClaims.groups,
    domains: provider?.domains.join(" ") ?? "",
    link_by_email: provider?.link_by_email ?? false,
    auto_provision: provider?.auto_provision ?? false,
    enabled: provider?.enabled ?? true,
  };
}

/** The request that creates the SAML provider a form describes, with one metadata source. */
export function samlBodyOf(values: SamlProviderForm): AuthProviderCreateWritable {
  const body: AuthProviderCreateWritable = {
    slug: values.slug.trim(),
    kind: "saml",
    display_name: values.display_name.trim(),
    enabled: values.enabled,
    domains: domainsOf(values.domains),
    link_by_email: values.link_by_email,
    auto_provision: values.auto_provision,
    sign_requests: values.sign_requests,
    name_id_format: values.name_id_format,
    claims: {
      email: values.claim_email.trim(),
      name: values.claim_name.trim(),
      groups: values.claim_groups.trim(),
    },
  };
  if (values.metadata_source === "url") body.idp_metadata_url = values.idp_metadata_url.trim();
  else body.idp_metadata_xml = values.idp_metadata_xml.trim();
  return body;
}

/**
 * The request that changes provider into what a form describes: only the
 * properties that differ. The metadata is named only when its source
 * changes or a new one is given, since naming it makes the server read it
 * again; an empty document keeps the one stored.
 */
export function samlPatchOf(
  provider: AuthProvider,
  values: SamlProviderForm,
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
  if (values.metadata_source === "url") {
    const url = values.idp_metadata_url.trim();
    if (url !== provider.idp_metadata_url) patch.idp_metadata_url = url;
  } else {
    const document = values.idp_metadata_xml.trim();
    if (document !== "") patch.idp_metadata_xml = document;
  }
  if (values.sign_requests !== provider.sign_requests) patch.sign_requests = values.sign_requests;
  if (values.name_id_format !== provider.name_id_format) {
    patch.name_id_format = values.name_id_format;
  }
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
  return patch;
}

/** The JSON Pointer of each field of the SAML form in the request body. */
export const samlPointerOf = {
  slug: "/slug",
  display_name: "/display_name",
  idp_metadata_url: "/idp_metadata_url",
  idp_metadata_xml: "/idp_metadata_xml",
  name_id_format: "/name_id_format",
  claim_email: "/claims/email",
  claim_name: "/claims/name",
  claim_groups: "/claims/groups",
  domains: "/domains",
} as const satisfies Partial<Record<keyof SamlProviderForm, string>>;

type SamlPointedField = keyof typeof samlPointerOf;

/** The sentence for the server's violation of a field of the SAML form, if it found one. */
export function samlFieldViolation(
  i18n: I18n,
  refusal: unknown,
  field: SamlPointedField,
): string | undefined {
  if (!(refusal instanceof ApiError)) return undefined;
  const violation = refusal.violations.find((v) => within(v.pointer, samlPointerOf[field]));
  if (!violation) return undefined;
  // A SAML provider reads attributes, which the server checks as claims.
  if (violation.code === "invalid_claim") {
    return i18n._(msg`Enter an attribute name of 1 to 256 characters.`);
  }
  return violationMessage(i18n, violation);
}

/** Whether the refusal's violations all sit on fields of the SAML form, which then say it themselves. */
export function samlFieldsExplain(refusal: unknown): boolean {
  if (!(refusal instanceof ApiError) || refusal.violations.length === 0) return false;
  const fields = Object.values(samlPointerOf);
  return refusal.violations.every((v) => fields.some((field) => within(v.pointer, field)));
}

/**
 * The SAML form's checks before it asks the server. The metadata is asked
 * for from the source chosen: a URL, or a pasted document, which an
 * existing provider whose document is stored may leave empty to keep it.
 */
export function samlFormSchema(
  i18n: I18n,
  { creating, stored }: { creating: boolean; stored?: AuthProvider },
) {
  const keepsDocument = stored !== undefined && pastedMetadata(stored);
  const attribute = z
    .string()
    .trim()
    .min(1, i18n._(msg`Enter an attribute name.`));
  return z
    .object({
      ...providerFields(i18n, { creating }),
      metadata_source: z.enum(["url", "document"]),
      idp_metadata_url: z.string(),
      idp_metadata_xml: z.string(),
      sign_requests: z.boolean(),
      name_id_format: z.enum(nameIDFormats),
      claim_email: attribute,
      claim_name: attribute,
      claim_groups: attribute,
    })
    .superRefine((values, ctx) => {
      if (values.metadata_source === "url") {
        const url = httpURLField(
          i18n,
          i18n._(msg`Enter the URL of the identity provider's metadata.`),
        ).safeParse(values.idp_metadata_url);
        for (const issue of url.error?.issues ?? []) {
          ctx.addIssue({ code: "custom", path: ["idp_metadata_url"], message: issue.message });
        }
      } else if (values.idp_metadata_xml.trim() === "" && !keepsDocument) {
        ctx.addIssue({
          code: "custom",
          path: ["idp_metadata_xml"],
          message: i18n._(msg`Paste the identity provider's metadata.`),
        });
      }
    });
}

/**
 * Where the instance serves its metadata for a SAML provider, on the
 * instance that serves the console, so the browser downloads it from
 * there.
 */
export function spMetadataPath(slug: string): string {
  return `/sso/${encodeURIComponent(slug)}/metadata`;
}
