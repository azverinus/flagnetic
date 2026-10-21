import { zodResolver } from "@hookform/resolvers/zod";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import type { ReactNode } from "react";
import { useForm } from "react-hook-form";
import { z } from "zod";

import { DialogForm } from "@/components/ui/dialog-form";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { ApiError } from "@/lib/errors";
import { formRefusal, violationMessage } from "@/lib/problem-messages";

/**
 * The server's bounds on a member's email and display name. The generated
 * schema states them only in its descriptions, so they are written here as
 * those give them.
 */
const emailMax = 254;
const displayNameMax = 200;

/** The fields of a member an instance admin sets. */
export type MemberValues = { email: string; display_name: string };

export type MemberFormProps = {
  /** The fields as they are; empty for a new member. */
  initial: MemberValues;
  submitLabel: ReactNode;
  pending: boolean;
  /** The refusal of the last submission. */
  refusal: unknown;
  onSubmit: (values: MemberValues) => void;
  onCancel: () => void;
};

/** The fields whose refusals are shown under them. */
const fieldPointers = ["/email", "/display_name"];

/**
 * A member's email and display name, in a dialog: inviting someone, or
 * editing a member. An email the instance already holds is said under the
 * email, where it is corrected.
 */
export function MemberForm({
  initial,
  submitLabel,
  pending,
  refusal,
  onSubmit,
  onCancel,
}: MemberFormProps) {
  const { i18n, t } = useLingui();
  const tooLong = (pointer: string, max: number) =>
    violationMessage(i18n, { pointer, code: "too_long", detail: "", params: { max } });
  const schema = z.object({
    email: z
      .string()
      .trim()
      .min(1, i18n._(msg`Enter an email address.`))
      .max(emailMax, tooLong("/email", emailMax))
      .refine(
        (value) => /^[^\s@]+@[^\s@]+$/.test(value),
        violationMessage(i18n, { pointer: "/email", code: "invalid_email", detail: "" }),
      ),
    display_name: z
      .string()
      .trim()
      .min(1, i18n._(msg`Enter a display name.`))
      .max(displayNameMax, tooLong("/display_name", displayNameMax)),
  });
  const form = useForm<MemberValues>({ resolver: zodResolver(schema), defaultValues: initial });
  const taken = refusal instanceof ApiError && refusal.code === "already_exists";
  const { errors } = form.formState;

  return (
    <DialogForm
      onSubmit={form.handleSubmit(onSubmit)}
      onCancel={onCancel}
      submitLabel={submitLabel}
      pending={pending}
      refusal={taken ? undefined : formRefusal(refusal, fieldPointers)}
    >
      <Field
        label={<Trans>Email</Trans>}
        error={
          errors.email?.message ??
          (taken ? t`Another member already has this email address.` : undefined)
        }
        refusal={refusal}
        pointer="/email"
      >
        <Input type="email" autoComplete="off" {...form.register("email")} />
      </Field>
      <Field
        label={<Trans>Display name</Trans>}
        error={errors.display_name?.message}
        refusal={refusal}
        pointer="/display_name"
      >
        <Input autoComplete="off" {...form.register("display_name")} />
      </Field>
    </DialogForm>
  );
}
