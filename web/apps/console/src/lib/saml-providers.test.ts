import type { AuthProvider } from "@flagnetic/api";
import { i18n } from "@lingui/core";
import { beforeEach, describe, expect, it } from "vitest";

import { activateForTests } from "@/test/render";

import { ApiError } from "./errors";
import {
  samlBodyOf,
  samlFieldsExplain,
  samlFieldViolation,
  samlFormOf,
  type SamlProviderForm,
  samlFormSchema,
  samlPatchOf,
  spMetadataPath,
} from "./saml-providers";

const provider: AuthProvider = {
  id: "0192f0c4-0000-7000-8000-000000000020",
  slug: "corp",
  kind: "saml",
  display_name: "Corp",
  enabled: true,
  domains: ["corp.example"],
  link_by_email: false,
  auto_provision: true,
  claims: { email: "email", name: "name", groups: "groups" },
  idp_metadata_url: "https://idp.corp.example/metadata",
  idp_entity_id: "https://idp.corp.example",
  idp_sso_url: "https://idp.corp.example/sso",
  idp_certificates: [{ sha256: "ab".repeat(32), not_after: "2029-01-01T00:00:00Z" }],
  idp_metadata_refreshed_at: "2026-11-08T10:00:00Z",
  sign_requests: true,
  name_id_format: "unspecified",
  acs_url: "https://flags.example/sso/corp/acs",
  entity_id: "https://flags.example/sso/corp/metadata",
  metadata_url: "https://flags.example/sso/corp/metadata",
  sp_certificates: [
    { sha256: "cd".repeat(32), not_after: "2029-11-01T00:00:00Z", status: "current" },
  ],
  rotation_pending: false,
  created_at: "2026-11-01T00:00:00Z",
  updated_at: "2026-11-01T00:00:00Z",
  version: 4,
};

/** The provider as if its metadata had been pasted: the document itself is never answered. */
const pasted: AuthProvider = (() => {
  const { idp_metadata_url: _, ...rest } = provider;
  return rest;
})();

const metadata = `<EntityDescriptor entityID="https://idp.corp.example"/>`;

beforeEach(() => activateForTests("en"));

describe("the form of a SAML provider", () => {
  it("starts a new provider from a metadata URL with the defaults", () => {
    expect(samlFormOf()).toEqual({
      slug: "",
      display_name: "",
      metadata_source: "url",
      idp_metadata_url: "",
      idp_metadata_xml: "",
      sign_requests: true,
      name_id_format: "unspecified",
      claim_email: "email",
      claim_name: "name",
      claim_groups: "groups",
      domains: "",
      link_by_email: false,
      auto_provision: false,
      enabled: true,
    });
  });

  it("holds what the provider answers", () => {
    expect(samlFormOf(provider)).toMatchObject({
      slug: "corp",
      metadata_source: "url",
      idp_metadata_url: "https://idp.corp.example/metadata",
      idp_metadata_xml: "",
      domains: "corp.example",
      auto_provision: true,
    });
  });

  it("knows pasted metadata by the URL it lacks, and never holds the document", () => {
    expect(samlFormOf(pasted)).toMatchObject({
      metadata_source: "document",
      idp_metadata_url: "",
      idp_metadata_xml: "",
    });
  });
});

describe("the request that creates a SAML provider", () => {
  const form: SamlProviderForm = {
    ...samlFormOf(),
    slug: " corp ",
    display_name: " Corp ",
    idp_metadata_url: " https://idp.corp.example/metadata ",
    idp_metadata_xml: "left over from the other choice",
    domains: "Corp.example corp.example.",
    name_id_format: "emailAddress",
  };

  it("sends the URL alone when the metadata is fetched", () => {
    expect(samlBodyOf(form)).toEqual({
      slug: "corp",
      kind: "saml",
      display_name: "Corp",
      enabled: true,
      domains: ["corp.example"],
      link_by_email: false,
      auto_provision: false,
      idp_metadata_url: "https://idp.corp.example/metadata",
      sign_requests: true,
      name_id_format: "emailAddress",
      claims: { email: "email", name: "name", groups: "groups" },
    });
  });

  it("sends the document alone when it is pasted", () => {
    const body = samlBodyOf({ ...form, metadata_source: "document", idp_metadata_xml: metadata });
    expect(body.idp_metadata_xml).toBe(metadata);
    expect(body).not.toHaveProperty("idp_metadata_url");
  });
});

describe("the request that edits a SAML provider", () => {
  it("is empty when nothing changed", () => {
    expect(samlPatchOf(provider, samlFormOf(provider))).toEqual({});
    expect(samlPatchOf(pasted, samlFormOf(pasted))).toEqual({});
  });

  it("names only what changed", () => {
    const patch = samlPatchOf(provider, {
      ...samlFormOf(provider),
      display_name: "Corporate",
      sign_requests: false,
      name_id_format: "persistent",
      claim_groups: "memberOf",
      link_by_email: true,
    });
    expect(patch).toEqual({
      display_name: "Corporate",
      sign_requests: false,
      name_id_format: "persistent",
      claims: { groups: "memberOf" },
      link_by_email: true,
    });
  });

  it("names a new metadata URL, which the server reads again", () => {
    expect(
      samlPatchOf(provider, {
        ...samlFormOf(provider),
        idp_metadata_url: "https://idp.corp.example/v2/metadata",
      }),
    ).toEqual({ idp_metadata_url: "https://idp.corp.example/v2/metadata" });
  });

  it("names a pasted document when one was pasted, and leaves the stored one otherwise", () => {
    expect(
      samlPatchOf(pasted, { ...samlFormOf(pasted), idp_metadata_xml: ` ${metadata} ` }),
    ).toEqual({ idp_metadata_xml: metadata });
  });

  it("switches a pasted document to a URL, and a URL to a pasted document", () => {
    expect(
      samlPatchOf(pasted, {
        ...samlFormOf(pasted),
        metadata_source: "url",
        idp_metadata_url: "https://idp.corp.example/metadata",
      }),
    ).toEqual({ idp_metadata_url: "https://idp.corp.example/metadata" });
    expect(
      samlPatchOf(provider, {
        ...samlFormOf(provider),
        metadata_source: "document",
        idp_metadata_xml: metadata,
      }),
    ).toEqual({ idp_metadata_xml: metadata });
  });
});

describe("the SAML form's checks", () => {
  const check = (values: Partial<SamlProviderForm>, creating = true, stored?: AuthProvider) =>
    samlFormSchema(i18n, { creating, stored }).safeParse({ ...samlFormOf(stored), ...values });
  // The first message of each field, as the form shows it.
  const messages = (result: ReturnType<typeof check>) =>
    result.success
      ? {}
      : Object.fromEntries(result.error.issues.toReversed().map((i) => [i.path[0], i.message]));

  it("asks for what a new provider needs", () => {
    expect(messages(check({}))).toEqual({
      slug: "Enter a slug.",
      display_name: "Enter a name.",
      idp_metadata_url: "Enter the URL of the identity provider's metadata.",
      domains: "Enter at least one email domain.",
    });
  });

  it("asks for the pasted document when the metadata is pasted", () => {
    expect(
      messages(
        check({
          slug: "corp",
          display_name: "Corp",
          domains: "corp.example",
          metadata_source: "document",
        }),
      ),
    ).toEqual({ idp_metadata_xml: "Paste the identity provider's metadata." });
  });

  it("refuses a metadata URL that is not an absolute http or https URL", () => {
    expect(
      messages(
        check({
          slug: "corp",
          display_name: "Corp",
          domains: "corp.example",
          idp_metadata_url: "idp.corp.example/metadata",
        }),
      ),
    ).toEqual({ idp_metadata_url: "Enter an absolute URL, such as https://login.example.com." });
  });

  it("keeps a pasted document that is stored when the field is left empty", () => {
    expect(check({}, false, pasted).success).toBe(true);
  });

  it("asks for a document when a provider fetched from a URL switches to a pasted one", () => {
    expect(messages(check({ metadata_source: "document" }, false, provider))).toEqual({
      idp_metadata_xml: "Paste the identity provider's metadata.",
    });
  });

  it("asks for every attribute name", () => {
    expect(messages(check({ claim_email: " " }, false, provider))).toEqual({
      claim_email: "Enter an attribute name.",
    });
  });
});

describe("the server's refusal of a SAML field", () => {
  const refusal = (pointer: string, code: string) =>
    new ApiError(422, {
      type: "about:blank",
      title: "Invalid",
      status: 422,
      code: "invalid_auth_provider",
      errors: [{ pointer, code, detail: "" }],
    });

  it.each([
    ["/idp_metadata_url", "insecure_url", "idp_metadata_url", /Use an https URL/],
    ["/idp_metadata_url", "no_redirect_binding", "idp_metadata_url", /HTTP-Redirect/],
    ["/idp_metadata_xml", "invalid_metadata", "idp_metadata_xml", /not the metadata/],
    ["/idp_metadata_xml", "no_signing_certificate", "idp_metadata_xml", /signing certificate/],
    ["/idp_metadata_xml", "metadata_too_large", "idp_metadata_xml", /1 MiB/],
    ["/claims/groups", "invalid_claim", "claim_groups", /attribute name/],
  ] as const)("says %s %s under its field", (pointer, code, field, message) => {
    expect(samlFieldViolation(i18n, refusal(pointer, code), field)).toMatch(message);
    expect(samlFieldsExplain(refusal(pointer, code))).toBe(true);
  });

  it("leaves a refusal outside the form's fields to the form", () => {
    expect(samlFieldsExplain(refusal("/issuer", "not_for_kind"))).toBe(false);
  });
});

describe("the SP metadata", () => {
  it("is downloaded from the instance that serves the console", () => {
    expect(spMetadataPath("corp")).toBe("/sso/corp/metadata");
  });
});
