import { Trans, useLingui } from "@lingui/react/macro";

import { formatDateTime } from "@/i18n/format";

/** A date and time in the member's language, or that there is none to show. */
export function Moment({ value }: { value: string | null }) {
  const { i18n } = useLingui();
  return value === null ? (
    <span className="text-muted-foreground">
      <Trans>Never</Trans>
    </span>
  ) : (
    <time dateTime={value} className="whitespace-nowrap">
      {formatDateTime(i18n, value)}
    </time>
  );
}
