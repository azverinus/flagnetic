import { Trans, useLingui } from "@lingui/react/macro";

import { formatDateTime, formatInstant, formatRelative } from "@/i18n/format";

/**
 * A date and time in the member's language, or that there is none to show.
 * With `relative` it reads as how long ago ("5 minutes ago") up to seven
 * days and as a short date after, the full time on hover and in the
 * element's title for screen readers.
 */
export function Moment({ value, relative = false }: { value: string | null; relative?: boolean }) {
  const { i18n } = useLingui();
  return value === null ? (
    <span className="text-muted-foreground">
      <Trans>Never</Trans>
    </span>
  ) : (
    <time
      dateTime={value}
      title={relative ? formatInstant(i18n, value) : undefined}
      className="whitespace-nowrap"
    >
      {relative ? formatRelative(i18n, value) : formatDateTime(i18n, value)}
    </time>
  );
}
