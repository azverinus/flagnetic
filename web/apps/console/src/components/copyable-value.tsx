import { Trans, useLingui } from "@lingui/react/macro";
import { CheckIcon, CopyIcon } from "lucide-react";
import { useId, useState } from "react";

import { Button } from "@/components/ui/button";

export type CopyableValueProps = {
  /** What the value is, such as "Redirect URI". */
  label: string;
  value: string;
};

/**
 * A value to copy into another system, such as the address an identity
 * provider is told: shown whole, selectable, with a button that puts it
 * on the clipboard and says so. A browser that refuses the clipboard
 * leaves the member the value on the screen.
 */
export function CopyableValue({ label, value }: CopyableValueProps) {
  const { t } = useLingui();
  const id = useId();
  const [copy, setCopy] = useState<"idle" | "copied" | "refused">("idle");
  const toClipboard = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopy("copied");
    } catch {
      setCopy("refused");
    }
  };
  return (
    <div className="flex flex-col gap-1.5">
      <span id={id} className="text-sm font-medium">
        {label}
      </span>
      <div className="flex items-start gap-2 rounded-md border bg-muted/40 p-3">
        <code
          aria-labelledby={id}
          className="min-w-0 flex-1 font-mono text-sm break-all select-all"
        >
          {value}
        </code>
        <Button
          variant="outline"
          size="sm"
          aria-label={t`Copy ${label}`}
          onClick={() => void toClipboard()}
        >
          <CopyIcon aria-hidden />
          <Trans>Copy</Trans>
        </Button>
      </div>
      <p role="status" className="min-h-4 text-xs text-muted-foreground">
        {copy === "copied" ? (
          <span className="inline-flex items-center gap-1 text-primary">
            <CheckIcon aria-hidden className="size-3" />
            <Trans>Copied to the clipboard.</Trans>
          </span>
        ) : copy === "refused" ? (
          <Trans>The browser refused the clipboard. Select the value and copy it by hand.</Trans>
        ) : null}
      </p>
    </div>
  );
}
