import { Trans, useLingui } from "@lingui/react/macro";
import { CircleAlertIcon } from "lucide-react";
import type { FormEvent, ReactNode } from "react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/cn";
import { ApiError } from "@/lib/errors";
import { problemMessage } from "@/lib/problem-messages";

export type FormPageProps = {
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  /** What the primary action is called, such as "Create project". */
  submitLabel: ReactNode;
  /** Cancelling, when the form has somewhere to go back to. */
  onCancel?: () => void;
  pending?: boolean;
  /** The refusal of the last submission, explained above the actions. */
  refusal?: unknown;
  className?: string;
  children: ReactNode;
};

/**
 * A form as a page: one width for every form, the fields at one spacing,
 * and the action row right-aligned with the primary action rightmost and
 * cancel before it.
 */
export function FormPage({
  onSubmit,
  submitLabel,
  onCancel,
  pending = false,
  refusal,
  className,
  children,
}: FormPageProps) {
  const { i18n } = useLingui();
  const error = refusal instanceof ApiError ? refusal : undefined;
  return (
    <form
      noValidate
      onSubmit={onSubmit}
      className={cn("flex w-full max-w-2xl flex-col gap-6", className)}
    >
      <div className="flex flex-col gap-5">{children}</div>
      {error ? (
        <p
          role="alert"
          className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm"
        >
          <CircleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
          {problemMessage(i18n, error)}
        </p>
      ) : null}
      <div className="flex flex-wrap justify-end gap-2">
        {onCancel ? (
          <Button type="button" variant="outline" onClick={onCancel}>
            <Trans>Cancel</Trans>
          </Button>
        ) : null}
        <Button type="submit" disabled={pending}>
          {submitLabel}
        </Button>
      </div>
    </form>
  );
}
