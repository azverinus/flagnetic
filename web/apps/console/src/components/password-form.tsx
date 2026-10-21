import { changeMyPassword } from "@flagnetic/api";
import { zodResolver } from "@hookform/resolvers/zod";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import { z } from "zod";

import { Field } from "@/components/ui/field";
import { FormPage } from "@/components/ui/form-page";
import { Input } from "@/components/ui/input";
import { unwrap } from "@/lib/errors";
import { formRefusal } from "@/lib/problem-messages";
import { mySessionsQuery } from "@/lib/queries";
import { forgetsSecret } from "@/lib/mutations";

/** The fields whose refusals are shown under them. */
const fieldPointers = ["/current_password", "/new_password"];

/**
 * Changing the member's password. Only the server judges the new password
 * against the policy, whose minimum an instance admin sets; the form asks
 * only for what is missing and that the new one was typed the same twice.
 */
export function PasswordForm() {
  const { i18n, t } = useLingui();
  const queryClient = useQueryClient();
  const schema = z
    .object({
      current_password: z.string().min(1, i18n._(msg`Enter your current password.`)),
      new_password: z.string().min(1, i18n._(msg`Enter a new password.`)),
      confirm_password: z.string(),
    })
    .refine((values) => values.new_password === values.confirm_password, {
      path: ["confirm_password"],
      message: i18n._(msg`The new passwords do not match.`),
    });
  type Values = z.infer<typeof schema>;
  const empty: Values = { current_password: "", new_password: "", confirm_password: "" };
  const form = useForm<Values>({ resolver: zodResolver(schema), defaultValues: empty });
  const change = useMutation({
    mutationFn: ({ current_password, new_password }: Values) =>
      unwrap(changeMyPassword({ body: { current_password, new_password } })),
    // Both passwords are in the variables.
    ...forgetsSecret,
    meta: { toast: false },
    onSuccess: () => {
      // Nothing typed lingers on the page once it is used.
      form.reset(empty);
      void queryClient.invalidateQueries({ queryKey: mySessionsQuery.queryKey });
      toast.success(t`Your password is changed. Every other device is signed out.`);
    },
  });
  const { errors } = form.formState;
  // A refusal of one of the fields is shown under it; any other above the actions.
  const refusal = formRefusal(change.error, fieldPointers);

  return (
    <FormPage
      onSubmit={form.handleSubmit((values) => change.mutate(values))}
      submitLabel={<Trans>Change password</Trans>}
      pending={change.isPending}
      refusal={refusal}
    >
      <Field
        label={<Trans>Current password</Trans>}
        error={errors.current_password?.message}
        refusal={change.error}
        pointer="/current_password"
      >
        <Input
          type="password"
          autoComplete="current-password"
          {...form.register("current_password")}
        />
      </Field>
      <Field
        label={<Trans>New password</Trans>}
        hint={
          <Trans>
            At least 12 characters, or as many as this instance asks for. Length is what makes a
            password hard to guess.
          </Trans>
        }
        error={errors.new_password?.message}
        refusal={change.error}
        pointer="/new_password"
      >
        <Input type="password" autoComplete="new-password" {...form.register("new_password")} />
      </Field>
      <Field
        label={<Trans>Confirm the new password</Trans>}
        error={errors.confirm_password?.message}
      >
        <Input type="password" autoComplete="new-password" {...form.register("confirm_password")} />
      </Field>
    </FormPage>
  );
}
