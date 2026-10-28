import type { Environment } from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";

/**
 * An environment's name as plain text, for where only text fits, such as
 * an option of a list: a protected environment says so, since a member
 * choosing where to look or write should know where changes are reviewed.
 */
export function environmentLabel(
  i18n: I18n,
  environment: Pick<Environment, "name" | "protected">,
): string {
  const { name } = environment;
  return environment.protected ? i18n._(msg`${name} (protected)`) : name;
}
