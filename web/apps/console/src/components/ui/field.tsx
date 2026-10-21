import { useLingui } from "@lingui/react/macro";
import { cloneElement, isValidElement, type ReactElement, type ReactNode, useId } from "react";

import { Label } from "@/components/ui/label";
import { cn } from "@/lib/cn";
import { ApiError } from "@/lib/errors";
import { violationsByPointer } from "@/lib/problem-messages";

type ControlProps = {
  id?: string;
  "aria-describedby"?: string;
  "aria-invalid"?: boolean;
};

export type FieldProps = {
  label: ReactNode;
  /** A sentence under the label saying what the field takes. */
  hint?: ReactNode;
  /** An error the form found itself, such as a required field left empty. */
  error?: string;
  /**
   * The refusal of the last submission and the JSON Pointer of this field
   * in the request body: the server's violation for it is shown here.
   */
  refusal?: unknown;
  pointer?: string;
  className?: string;
  /** The one control the label names. */
  children: ReactElement<ControlProps>;
};

/**
 * One form field: the label above, then the hint, the control and the
 * error, at one spacing. The control is wired to all three, so a screen
 * reader names it by the label and reads the hint and the error with it.
 */
export function Field({ label, hint, error, refusal, pointer, className, children }: FieldProps) {
  const { i18n } = useLingui();
  const fallbackId = useId();
  const id = (isValidElement(children) && children.props.id) || fallbackId;
  const hintId = `${id}-hint`;
  const errorId = `${id}-error`;
  const message =
    error ??
    (refusal instanceof ApiError && pointer
      ? violationsByPointer(i18n, refusal).get(pointer)
      : undefined);
  const describedBy = [hint ? hintId : null, message ? errorId : null].filter(Boolean).join(" ");

  return (
    <div className={cn("flex flex-col gap-2", className)}>
      <Label htmlFor={id}>{label}</Label>
      {hint ? (
        <p id={hintId} className="text-sm text-muted-foreground">
          {hint}
        </p>
      ) : null}
      {cloneElement(children, {
        id,
        "aria-describedby": describedBy || undefined,
        "aria-invalid": message ? true : undefined,
      })}
      {message ? (
        <p id={errorId} className="text-sm text-destructive">
          {message}
        </p>
      ) : null}
    </div>
  );
}
