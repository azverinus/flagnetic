import type { Violation } from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";

import { formatLifetime, formatNumber, formatWait } from "@/i18n/format";

import { permissionName } from "./access-names";
import { ApiError } from "./errors";

/**
 * Server refusals in the member's language. The server names every refusal
 * by a stable code and gives the values it depends on as members of the
 * problem document; the console writes the sentence. The server's English
 * `detail` is shown only for a code this table does not know yet.
 */

/** How long the refusal asks to wait, in the member's language, if it says. */
export function retryWait(i18n: I18n, error: ApiError): string | undefined {
  return error.retryAfter === undefined ? undefined : formatWait(i18n, error.retryAfter);
}

/** The sentence for a refusal of the management API. */
export function problemMessage(i18n: I18n, error: ApiError): string {
  const retry = retryWait(i18n, error);
  switch (error.code) {
    case "invalid_credentials":
      return i18n._(msg`The email or the password is not right.`);
    case "locked":
      return retry
        ? i18n._(msg`Too many failed attempts for this email. Try again in ${retry}.`)
        : i18n._(msg`Too many failed attempts for this email. Try again later.`);
    case "rate_limited":
      return retry
        ? i18n._(msg`Too many requests. Try again in ${retry}.`)
        : i18n._(msg`Too many requests. Try again later.`);
    case "busy":
      return i18n._(msg`The server is busy checking other passwords. Try again in a moment.`);
    case "session_expired":
      return i18n._(msg`Your session has ended. Sign in again to continue.`);
    case "unauthenticated":
      return i18n._(msg`You are not signed in.`);
    case "cross_origin":
      return i18n._(msg`The request came from another site and was refused.`);
    case "session_required":
      return i18n._(msg`Only a signed-in session can do this, not an API token.`);
    case "insufficient_scope":
      return i18n._(msg`The token in use does not carry the scope this needs.`);
    case "forbidden": {
      const permission = error.problem.permission;
      if (typeof permission === "string") {
        const name = permissionName(i18n, permission);
        return i18n._(msg`You need the permission to ${name}.`);
      }
      return i18n._(msg`You may not do this.`);
    }
    case "not_found":
      return i18n._(msg`It does not exist, or it was deleted.`);
    case "invalid_request":
      return i18n._(msg`The server could not read the request.`);
    case "request_too_large":
      return i18n._(msg`The request is too large.`);
    case "unsupported_media_type":
      return i18n._(msg`The request was not sent as JSON.`);
    case "unreachable":
      return i18n._(msg`The server could not be reached. Check your connection and try again.`);
    case "unavailable":
      return retry
        ? i18n._(msg`The server is unavailable. Try again in ${retry}.`)
        : i18n._(msg`The server is unavailable. Try again in a moment.`);
    case "internal":
      return i18n._(msg`Something went wrong on the server.`);
    case "already_exists":
      return i18n._(msg`One with this key already exists.`);
    case "last_environment":
      return i18n._(
        msg`This is the project's last environment. A project always keeps one to serve from.`,
      );
    case "standard_attribute":
      return i18n._(msg`Standard attributes are part of every project, so they cannot be deleted.`);
    case "type_immutable":
      return i18n._(msg`An attribute's type is fixed once it is declared.`);
    case "version_mismatch":
      return i18n._(
        msg`Someone else changed this since you opened it. Reload to see their changes, then make yours again.`,
      );
    case "key_immutable":
      return i18n._(msg`A key never changes once it is set.`);
    case "scope_not_allowed":
      return i18n._(msg`A token cannot carry a scope you do not hold yourself.`);
    case "last_instance_admin":
      return i18n._(
        msg`This is the instance's last active instance admin. Make another member instance admin first.`,
      );
    case "member_not_invited":
      return i18n._(msg`Only an invited member has an invitation to replace.`);
    case "member_deactivated":
      return i18n._(msg`The member is deactivated. Reactivate them first.`);
    case "already_granted":
      return i18n._(msg`The member already holds this role in the project.`);
    case "role_not_allowed": {
      const permission = error.problem.permission;
      if (typeof permission === "string") {
        const name = permissionName(i18n, permission);
        return i18n._(
          msg`The role includes the permission to ${name}, which you do not hold yourself here.`,
        );
      }
      return i18n._(msg`The role includes a permission you do not hold yourself here.`);
    }
    case "invitation_gone":
      return i18n._(
        msg`This invitation link no longer works: it was used, replaced by a newer one, or it expired.`,
      );
    default:
      if (error.code.startsWith("invalid_") && error.violations.length > 0) {
        return i18n._(msg`Some fields need correcting.`);
      }
      return error.detail || i18n._(msg`The request was refused.`);
  }
}

function param(violation: Violation, name: string): unknown {
  return violation.params?.[name];
}

function numberParam(i18n: I18n, violation: Violation, name: string): string | undefined {
  const value = param(violation, name);
  return typeof value === "number" ? formatNumber(i18n, value) : undefined;
}

/** The sentence for one field violation, shown under its field. */
export function violationMessage(i18n: I18n, violation: Violation): string {
  switch (violation.code) {
    case "required":
      return i18n._(msg`Required.`);
    case "invalid_key":
      return i18n._(
        msg`Start with a lowercase letter; then lowercase letters, digits, - and _, at most 64 in all.`,
      );
    case "invalid_name":
      return i18n._(msg`Enter a name.`);
    case "invalid_email":
      return i18n._(msg`Enter a valid email address.`);
    case "too_long": {
      const max = numberParam(i18n, violation, "max");
      return max ? i18n._(msg`At most ${max} characters.`) : i18n._(msg`Too long.`);
    }
    case "reserved_key":
      return i18n._(msg`This key is reserved.`);
    case "invalid_type":
      return i18n._(msg`Choose one of the offered types.`);
    case "unknown_scope":
      return i18n._(msg`Choose one of the offered scopes.`);
    case "out_of_range": {
      const min = numberParam(i18n, violation, "min");
      const max = numberParam(i18n, violation, "max");
      return min && max
        ? i18n._(msg`Enter a value from ${min} to ${max}.`)
        : i18n._(msg`The value is out of range.`);
    }
    case "invalid_credentials":
      // Only a password change names a field with it: the current password.
      return i18n._(msg`That is not your current password.`);
    case "unknown_member":
      return i18n._(msg`Choose one of the offered members.`);
    case "unknown_role":
      return i18n._(msg`Choose one of the offered roles.`);
    case "unknown_locale":
      return i18n._(msg`Choose one of the offered languages.`);
    case "scope_not_allowed":
      return i18n._(msg`You do not hold this scope yourself.`);
    case "in_past":
      return i18n._(msg`Choose a time in the future.`);
    case "beyond_max_lifetime": {
      const max = param(violation, "max_lifetime_seconds");
      if (typeof max === "number") {
        const lifetime = formatLifetime(i18n, max);
        return i18n._(msg`This instance allows a token at most ${lifetime} ahead.`);
      }
      return i18n._(msg`This is further ahead than this instance allows.`);
    }
    case "duplicate":
      return i18n._(msg`This is already in the list.`);
    case "too_many": {
      const max = numberParam(i18n, violation, "max");
      return max ? i18n._(msg`At most ${max}.`) : i18n._(msg`Too many.`);
    }
    case "invalid_value":
      return i18n._(msg`This value is not valid here.`);
    case "invalid_owner":
      return i18n._(
        msg`The owner must be an active member holding a role in the project, or an instance admin.`,
      );
    case "idle_beyond_absolute":
      return i18n._(msg`The idle timeout cannot be longer than the absolute timeout.`);
    case "weak_password":
      switch (param(violation, "rule")) {
        case "length": {
          const min = numberParam(i18n, violation, "min");
          const max = numberParam(i18n, violation, "max");
          return min && max
            ? i18n._(msg`Use ${min} to ${max} characters.`)
            : i18n._(msg`The password is too short or too long.`);
        }
        case "email":
          return i18n._(msg`The password must not contain your email address.`);
        case "common":
          return i18n._(msg`This password is too common. Choose another.`);
        default:
          return i18n._(msg`Choose a stronger password.`);
      }
    default:
      return violation.detail;
  }
}

/**
 * The violations of a refusal by the field they concern, a JSON Pointer
 * into the request body, for a form to show under each field.
 */
export function violationsByPointer(i18n: I18n, error: ApiError): Map<string, string> {
  const out = new Map<string, string>();
  for (const violation of error.violations) {
    if (!out.has(violation.pointer)) out.set(violation.pointer, violationMessage(i18n, violation));
  }
  return out;
}

/**
 * The refusal a form explains above its actions: none when it concerns
 * one of the form's fields, since that field says it under itself, and
 * the refusal as it is otherwise.
 */
export function formRefusal(refusal: unknown, fieldPointers: readonly string[]): unknown {
  return refusal instanceof ApiError &&
    refusal.violations.some((violation) => fieldPointers.includes(violation.pointer))
    ? undefined
    : refusal;
}
