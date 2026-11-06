import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";

/**
 * The sentence for the error a failed single sign-on sends the member back
 * to the sign-in page with (`/sign-in?error=`). Each is a stable code; the
 * page says what happened and whom to ask, and never what the identity
 * provider answered, since a page anyone can open must not echo it. An
 * enforced email sent to the wrong door names the provider it must use.
 */
export function ssoErrorMessage(i18n: I18n, code: string, providerName?: string): string {
  switch (code) {
    case "sso_failed":
      return i18n._(
        msg`Signing in through the identity provider failed. Try again; if it keeps failing, ask your instance admin to check the provider.`,
      );
    case "idp_error":
      return i18n._(
        msg`The identity provider refused the sign-in. Ask whoever runs it, or your instance admin.`,
      );
    case "member_not_found":
      return i18n._(
        msg`No member of this instance matches that account. Ask an instance admin to invite you.`,
      );
    case "member_deactivated":
      return i18n._(msg`Your account here is deactivated. Ask an instance admin to reactivate it.`);
    case "attempt_mismatch":
      return i18n._(
        msg`That sign-in was started in another browser or window, so it was not completed. Start it again from here.`,
      );
    case "sso_required":
      return providerName
        ? i18n._(msg`Your email address signs in through ${providerName} only. Use its button.`)
        : i18n._(
            msg`Your email address signs in through your company's identity provider only. Use its button.`,
          );
    default:
      return i18n._(msg`Signing in did not work (${code}). Try again, or ask your instance admin.`);
  }
}
