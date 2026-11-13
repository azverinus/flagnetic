import type { Violation } from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";

import { formatLifetime, formatNumber, formatWait } from "@/i18n/format";

import { permissionName } from "./access-names";
import { ApiError } from "./errors";
import { failureText } from "./sinks";

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
    case "ruleset_unavailable":
      return retry
        ? i18n._(msg`This server cannot load the ruleset yet. Try again in ${retry}.`)
        : i18n._(msg`This server cannot load the ruleset yet. Try again in a moment.`);
    case "internal":
      return i18n._(msg`Something went wrong on the server.`);
    case "already_exists":
      return i18n._(msg`One with this key already exists.`);
    case "last_environment":
      return i18n._(
        msg`This is the project's last environment. A project always keeps one to serve from.`,
      );
    case "environment_protected":
      return i18n._(
        msg`A protected environment cannot be deleted. Stop protecting it on its page first.`,
      );
    case "standard_attribute":
      return i18n._(msg`Standard attributes are part of every project, so they cannot be deleted.`);
    case "type_immutable":
      return i18n._(msg`An attribute's type is fixed once it is declared.`);
    case "version_mismatch":
      return i18n._(
        msg`Someone else changed this since you opened it. Reload to see their changes, then make yours again.`,
      );
    case "in_use": {
      const references = referenceList(i18n, error);
      return references
        ? i18n._(msg`It is still in use by ${references}.`)
        : i18n._(msg`It is still in use.`);
    }
    case "version_not_found":
      return i18n._(msg`The configuration's history in this environment has no such version.`);
    case "not_archived":
      return i18n._(msg`Archive the flag first; only an archived flag can be deleted.`);
    case "archived":
      return i18n._(msg`The flag is archived. Restore it to change its configurations.`);
    case "not_draft":
      return i18n._(msg`Only a draft experiment can be changed this way.`);
    case "frozen":
      return i18n._(
        msg`The experiment's status no longer lets these fields change: its flag, unit type and weights stay as they started.`,
      );
    case "traffic_allocation_decrease":
      return i18n._(msg`A running experiment's traffic allocation can only grow.`);
    case "not_running":
      return i18n._(msg`Only a running experiment can be stopped.`);
    case "running":
      return i18n._(msg`A running experiment is stopped with a decision before it is archived.`);
    case "not_stopped":
      return i18n._(msg`Only a stopped experiment has a decision note.`);
    case "experiment_not_ready":
      return i18n._(msg`The experiment is not ready to start.`);
    case "change_request_required":
      return i18n._(
        msg`The environment is protected: this change goes live only through an approved change request.`,
      );
    case "base_moved":
      return i18n._(
        msg`The configuration changed after this change request was based on it. Rebase or copy it onto the current one.`,
      );
    case "invalid_state":
      return i18n._(msg`The change request's state no longer allows this.`);
    case "not_author":
      return i18n._(msg`Only the change request's author may do this.`);
    case "author_cannot_approve":
      return i18n._(msg`You cannot approve or reject your own change request.`);
    case "token_cannot_approve":
      return i18n._(msg`A project token never approves or rejects change requests.`);
    case "scheduled":
      return i18n._(msg`The change request is applied at its scheduled time, not by hand.`);
    case "not_a_rollout":
      return i18n._(msg`Only a rule serving a rollout has a salt.`);
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
    case "member_invited":
      return i18n._(
        msg`The member has not accepted their invitation yet. Give them this once they have joined.`,
      );
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
    case "sdk_key_limit_reached":
      return i18n._(
        msg`The environment already has 10 active keys of this kind. Revoke one, or wait for a rotated one to end, first.`,
      );
    case "sdk_key_not_active":
      return i18n._(msg`Only an active key can be rotated. This one was rotated or revoked.`);
    case "webhook_disabled":
      return i18n._(msg`This webhook is disabled. Enable it first; it is sent nothing until then.`);
    case "insecure_url":
      return i18n._(
        msg`This instance accepts only https targets. Use an https URL, or ask the operator to allow plain http.`,
      );
    case "private_network_target":
      return i18n._(
        msg`This instance does not reach targets on its own network. Use a public address, or ask the operator to allow private networks.`,
      );
    case "unknown_event_type":
      return i18n._(msg`One of the event types is not in the catalogue.`);
    case "link_local_target":
      return i18n._(
        msg`This target is on a link-local address, which the instance never reaches. Use another address.`,
      );
    case "unknown_environment":
      return i18n._(msg`One of the environments does not exist in this project.`);
    case "no_secret_to_rotate":
      return i18n._(
        msg`Only a webhook sink has a secret the server draws. Change this sink's password or secret access key by editing it.`,
      );
    case "sink_not_ready": {
      const failure = failureText(i18n, {
        class: typeof error.problem.failure === "string" ? error.problem.failure : null,
        status:
          typeof error.problem.failure_status === "number" ? error.problem.failure_status : null,
        code: typeof error.problem.failure_code === "string" ? error.problem.failure_code : null,
      });
      return i18n._(msg`The sink is not ready to be enabled: ${failure}.`);
    }
    case "rows_expired":
      return i18n._(
        msg`Retention has dropped every row of this dead letter, so there is nothing left to send.`,
      );
    case "invalid_replay_range":
      return i18n._(msg`The end of a replay cannot be before its start.`);
    case "out_of_retention":
      return i18n._(msg`That time is before the oldest event still kept. Choose a later one.`);
    case "kind_immutable":
      return i18n._(msg`A sink's kind cannot be changed. Create a new sink of the other kind.`);
    case "export_too_large":
      return i18n._(
        msg`Too many entries match these filters to download at once. Narrow them, for example with From and To, and try again.`,
      );
    case "base_url_required":
      return i18n._(
        msg`Set the instance's public address first (FLAGNETIC_BASE_URL): the addresses the identity provider is told are built from it.`,
      );
    case "provider_unreachable": {
      const failure = error.problem.failure;
      return typeof failure === "string" && failure !== ""
        ? i18n._(
            msg`The identity provider could not be reached or did not answer as expected: ${failure}`,
          )
        : i18n._(msg`The identity provider could not be reached or did not answer as expected.`);
    }
    case "rotation_pending":
      return i18n._(
        msg`A certificate rotation is already pending. Complete it before starting another.`,
      );
    case "no_rotation_pending":
      return i18n._(msg`No certificate rotation is pending: start one first.`);
    case "not_saml":
      return i18n._(msg`Only a SAML provider has a certificate to rotate.`);
    case "provider_in_use": {
      const uses = providerUses(i18n, error);
      return uses
        ? i18n._(
            msg`The provider is still used by ${uses}. Disable it instead: that stops its sign-ins and keeps its members, identities and grants.`,
          )
        : i18n._(
            msg`The provider is still in use. Disable it instead: that stops its sign-ins and keeps its members, identities and grants.`,
          );
    }
    case "sso_sourced":
      return i18n._(
        msg`An auth provider's group mapping made this, so only the provider takes it away. An instance admin changes its group mappings on the provider's page.`,
      );
    case "last_auth_method":
      return i18n._(
        msg`That is the only usable way to sign in left, so it stays. Add another way in first: link a provider, or keep the password.`,
      );
    case "provider_enforced":
      return i18n._(
        msg`The email address's domain signs in through this provider only, so its identity there cannot be unlinked.`,
      );
    case "member_provisioned":
      return i18n._(
        msg`The provider manages this member, so their identity at it stays for as long as they are provisioned. Deactivate or remove them at the provider.`,
      );
    case "reset_gone":
      return i18n._(
        msg`This password reset link no longer works: it was used, ended by a password change, or it expired.`,
      );
    case "member_not_active":
      return i18n._(
        msg`The member has not joined yet, so they have no password to reset. Issue a new invitation link instead.`,
      );
    case "identity_required":
      return i18n._(
        msg`Local login stays on until you hold an identity of your own at an enabled auth provider, so that you cannot lock yourself out. Sign in through a provider or link one to your account, then try again.`,
      );
    case "local_login_disabled":
      return i18n._(
        msg`This instance signs nobody in with a password. Sign in through your identity provider instead.`,
      );
    case "sso_required":
      return i18n._(
        msg`Your email address signs in through your company's identity provider only, so a password would not let you in.`,
      );
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

/**
 * What an `in_use` refusal names, joined as the member's language joins a
 * list: each flag or segment by its key, with the environment it is named
 * in when it is one.
 */
function referenceList(i18n: I18n, error: ApiError): string | undefined {
  const references = error.problem.references;
  if (!Array.isArray(references) || references.length === 0) return undefined;
  const named = references.map(({ kind, key, environment }) => {
    const what =
      kind === "flag"
        ? i18n._(msg`the flag ${key}`)
        : kind === "segment"
          ? i18n._(msg`the segment ${key}`)
          : key;
    return environment ? i18n._(msg`${what} in ${environment}`) : what;
  });
  return new Intl.ListFormat(i18n.locale, { type: "conjunction" }).format(named);
}

/**
 * What a `provider_in_use` refusal says still depends on the provider,
 * joined as the member's language joins a list. The server names each use
 * in English; the ones it names today are translated, any other is shown
 * as it came.
 */
function providerUses(i18n: I18n, error: ApiError): string | undefined {
  const uses = error.problem.used_by;
  if (!Array.isArray(uses) || uses.length === 0) return undefined;
  const named = uses.map((use) => {
    switch (use) {
      case "identities":
        return i18n._(msg`linked identities`);
      case "SCIM tokens":
        return i18n._(msg`SCIM tokens`);
      case "provisioned members":
        return i18n._(msg`provisioned members`);
      case "grants":
        return i18n._(msg`grants made by its group mappings`);
      case "an enforced domain":
        return i18n._(msg`an enforced domain`);
      default:
        return String(use);
    }
  });
  return new Intl.ListFormat(i18n.locale, { type: "conjunction" }).format(named);
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
    case "too_deep": {
      const max = numberParam(i18n, violation, "max");
      return max
        ? i18n._(msg`Nest arrays and objects at most ${max} deep.`)
        : i18n._(msg`Nested too deep.`);
    }
    case "too_complex":
      return i18n._(
        msg`The pattern repeats too much. Shorten its repetitions or split it into conditions.`,
      );
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
    case "unknown_project":
      return i18n._(msg`Choose one of the offered projects.`);
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
    case "server_key_origins":
      return i18n._(msg`A server key is never used from a browser, so it takes no origins.`);
    case "invalid_origin":
      return i18n._(
        msg`Enter a scheme, a host and an optional port, such as https://app.example.com.`,
      );
    case "invalid_value":
      return i18n._(msg`This value is not valid here.`);
    case "unknown_flag":
      return i18n._(msg`The project has no such flag.`);
    case "flag_archived":
      return i18n._(msg`The flag is archived; choose one that is not.`);
    case "undeclared_unit_type":
      return i18n._(msg`The project declares no such unit type.`);
    case "unknown_variation":
      return i18n._(msg`The flag has no such variation.`);
    case "invalid_weight":
      return i18n._(msg`Enter a percentage from 0 to 100, at most two decimals.`);
    case "invalid_weights_sum":
      return i18n._(msg`The weights must sum to 100.00 %.`);
    case "invalid_count":
      return i18n._(msg`Compare 2 to 50 variations.`);
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
    case "invalid_slug":
      return i18n._(
        msg`Start with a lowercase letter; then lowercase letters, digits, - and _, at most 64 in all.`,
      );
    case "reserved_slug":
      return i18n._(msg`This slug is reserved.`);
    case "invalid_display_name":
      return i18n._(msg`Enter a name of 1 to 200 characters.`);
    case "domains_required":
      return i18n._(msg`Enter at least one email domain.`);
    case "too_many_domains": {
      const max = numberParam(i18n, violation, "max");
      return max ? i18n._(msg`At most ${max} domains.`) : i18n._(msg`Too many domains.`);
    }
    case "unknown_provider":
      return i18n._(msg`Choose one of the offered providers.`);
    case "provider_disabled":
      return i18n._(msg`This provider is disabled: enable it first, or choose another.`);
    case "not_provider_domain":
      return i18n._(
        msg`This provider does not list the domain: add it to the provider's domains first.`,
      );
    case "duplicate_domain":
      return i18n._(msg`This domain is already enforced.`);
    case "invalid_domain":
      return i18n._(msg`Enter host names such as example.com, without an at sign or a scheme.`);
    case "invalid_issuer":
      return i18n._(
        msg`Enter the http or https URL of the identity provider, without credentials, a query or a fragment.`,
      );
    case "insecure_url":
      // A webhook sink's target is refused for the instance's setting, not a loopback rule.
      if (violation.pointer.startsWith("/webhook/")) {
        return i18n._(
          msg`This instance accepts only https targets. Use an https URL, or ask the operator to allow plain http.`,
        );
      }
      return i18n._(
        msg`Use an https URL. Plain http is accepted only on a loopback address such as localhost.`,
      );
    case "private_network_target":
      return i18n._(
        msg`This instance does not reach targets on its own network. Use a public address, or ask the operator to allow private networks.`,
      );
    case "link_local_target":
      return i18n._(
        msg`This target is on a link-local address, which the instance never reaches. Use another address.`,
      );
    case "invalid_url":
      // A webhook's URL may carry a query; a ClickHouse URL may not, since
      // the platform builds the query of every insert itself.
      if (violation.pointer.startsWith("/clickhouse/")) {
        return i18n._(
          msg`Enter an absolute http or https URL without credentials, a query or a fragment.`,
        );
      }
      return i18n._(msg`Enter an absolute http or https URL without credentials or a fragment.`);
    case "invalid_table_prefix":
      return i18n._(msg`Use letters, digits and underscores, not starting with a digit.`);
    case "invalid_endpoint":
      return i18n._(
        msg`Enter a scheme, a host and an optional port, such as https://s3.example.com.`,
      );
    case "invalid_bucket":
      return i18n._(msg`Use letters, digits, dots, hyphens and underscores.`);
    case "invalid_format":
      return i18n._(msg`Choose JSON lines or Parquet.`);
    case "invalid_compression":
      return i18n._(msg`This compression does not fit the format. Parquet files are always zstd.`);
    case "invalid_header":
      return i18n._(
        msg`A header has a name of letters, digits and symbols without spaces, and a value on one line.`,
      );
    case "reserved":
      return violation.pointer.includes("/headers/")
        ? i18n._(msg`The X-Flagnetic- headers are the platform's own.`)
        : i18n._(msg`Delivery sets this setting itself.`);
    case "invalid_event_kind":
      return i18n._(msg`Choose exposures, aliases or evaluation summaries.`);
    case "unknown_environment":
      return i18n._(msg`The project has no environment with this key.`);
    case "metadata_required":
      return i18n._(msg`Enter the URL of the identity provider's metadata, or paste it.`);
    case "metadata_conflict":
      return i18n._(msg`Give the metadata as a URL or pasted, not both.`);
    case "metadata_too_large":
      return i18n._(msg`The metadata is at most 1 MiB.`);
    case "invalid_metadata_url":
      return i18n._(
        msg`Enter the http or https URL of the metadata, without credentials or a fragment.`,
      );
    case "invalid_metadata":
      return i18n._(
        msg`This is not the metadata of a SAML identity provider the instance can use: check that it is the identity provider's EntityDescriptor, without a DOCTYPE, with readable certificates and an https sign-on address.`,
      );
    case "no_redirect_binding":
      return i18n._(
        msg`The identity provider offers no single sign-on service with the HTTP-Redirect binding, which the instance signs in through.`,
      );
    case "no_signing_certificate":
      return i18n._(msg`The metadata holds no signing certificate to check responses with.`);
    case "unknown_name_id_format":
      return i18n._(msg`Choose one of the offered name identifier formats.`);
    case "invalid_client_id":
      return i18n._(msg`Enter a client ID of 1 to 512 characters.`);
    case "invalid_client_secret":
      return i18n._(msg`Enter a client secret of 1 to 4096 characters.`);
    case "too_many_scopes": {
      const max = numberParam(i18n, violation, "max");
      return max ? i18n._(msg`At most ${max} scopes.`) : i18n._(msg`Too many scopes.`);
    }
    case "invalid_scope":
      return i18n._(msg`A scope is a word without spaces.`);
    case "invalid_claim":
      return i18n._(msg`Enter a claim name of 1 to 256 characters.`);
    case "invalid_host":
      return i18n._(msg`Enter a host name or address of at most 253 characters, without spaces.`);
    case "unknown_security":
      return i18n._(msg`Choose one of the offered security modes.`);
    case "invalid_sender":
      return i18n._(
        msg`Enter one address, optionally with a name, such as Flagnetic <flags@example.com>.`,
      );
    case "invalid_username":
      return i18n._(msg`Enter a username of at most 256 characters on one line.`);
    case "credentials_together":
      // The server names the field that is missing its partner.
      return violation.pointer === "/username"
        ? i18n._(msg`A password needs a username.`)
        : i18n._(msg`A username needs a password.`);
    case "insecure_credentials":
      return i18n._(
        msg`A username and password are sent only over STARTTLS or TLS, unless the SMTP server is this machine.`,
      );
    case "blank":
      return i18n._(msg`Enter the directory group.`);
    case "exclusive":
      return i18n._(msg`Instance admin stands alone: choose it or a role in a project, not both.`);
    case "too_soon":
      return i18n._(msg`Choose a later time: the server needs some lead to apply it.`);
    case "too_late":
      return i18n._(msg`Choose a time at most a year ahead.`);
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
