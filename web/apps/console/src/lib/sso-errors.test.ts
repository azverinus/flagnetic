import { i18n } from "@lingui/core";
import { beforeEach, describe, expect, it } from "vitest";

import { activateForTests } from "@/test/render";

import { linkErrorMessage, ssoErrorMessage } from "./sso-errors";

beforeEach(() => activateForTests("en"));

describe("the sentence for an error the sign-in page was sent with", () => {
  it.each([
    ["sso_failed", "Signing in through the identity provider failed"],
    ["idp_error", "The identity provider refused the sign-in"],
    ["member_not_found", "No member of this instance matches that account"],
    ["member_deactivated", "Your account here is deactivated"],
    ["attempt_mismatch", "That sign-in was started in another browser"],
  ])("explains %s", (code, start) => {
    expect(ssoErrorMessage(i18n, code)).toMatch(new RegExp(`^${start}`));
  });

  it("names the provider an enforced email must sign in through", () => {
    expect(ssoErrorMessage(i18n, "sso_required", "Acme SSO")).toBe(
      "Your email address signs in through Acme SSO only. Use its button.",
    );
    expect(ssoErrorMessage(i18n, "sso_required")).toBe(
      "Your email address signs in through your company's identity provider only. Use its button.",
    );
  });

  it("says that signing in did not work for a code it does not know, showing the code", () => {
    expect(ssoErrorMessage(i18n, "something_new")).toBe(
      "Signing in did not work (something_new). Try again, or ask your instance admin.",
    );
  });
});

describe("the sentence for an error the personal settings were sent with after linking", () => {
  it.each([
    [
      "identity_linked",
      "That account at the identity provider is already linked to another member",
    ],
    ["attempt_mismatch", "That linking was started in another browser"],
    ["sso_failed", "Linking through the identity provider failed"],
    ["idp_error", "The identity provider refused the request"],
  ])("explains %s, and says nothing was linked", (code, start) => {
    const sentence = linkErrorMessage(i18n, code);
    expect(sentence).toMatch(new RegExp(`^${start}`));
    expect(sentence).toContain("nothing was linked");
  });

  it("shows the code of an error it does not know", () => {
    expect(linkErrorMessage(i18n, "something_new")).toBe(
      "Linking did not work (something_new). Try again, or ask your instance admin.",
    );
  });
});
