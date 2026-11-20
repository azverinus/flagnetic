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

/**
 * The names of the protected environments a flag is switched on in. A
 * protected environment refuses every write that changes what its SDKs
 * receive, and archiving or restoring a flag that is on there does, so
 * neither is offered while this lists any.
 */
export function protectedWhereOn(
  flag: { environments: readonly { environment: string; on: boolean }[] },
  environments: readonly Pick<Environment, "key" | "name" | "protected">[],
): string[] {
  return environments.flatMap((environment) =>
    environment.protected &&
    flag.environments.some((entry) => entry.environment === environment.key && entry.on)
      ? [environment.name]
      : [],
  );
}
