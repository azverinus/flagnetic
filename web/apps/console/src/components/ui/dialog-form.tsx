import type { FormEvent, ReactNode } from "react";

import { FormActions, FormRefusal } from "@/components/ui/form-page";

export type DialogFormProps = {
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  onCancel: () => void;
  submitLabel: ReactNode;
  pending?: boolean;
  /**
   * The refusal of the last submission, explained above the actions. A
   * refusal the form shows under a field instead is left out by the caller.
   */
  refusal?: unknown;
  children: ReactNode;
};

/**
 * A short form inside a dialog, such as creating or editing one row of a
 * table: the fields, then the refusal and the action row a form page has.
 * Rendered as the dialog's content, it is mounted only while the dialog is
 * open, so every opening starts from a fresh form.
 */
export function DialogForm({
  onSubmit,
  onCancel,
  submitLabel,
  pending = false,
  refusal,
  children,
}: DialogFormProps) {
  return (
    <form noValidate onSubmit={onSubmit} className="flex flex-col gap-5">
      {children}
      <FormRefusal refusal={refusal} />
      <FormActions submitLabel={submitLabel} onCancel={onCancel} pending={pending} />
    </form>
  );
}
