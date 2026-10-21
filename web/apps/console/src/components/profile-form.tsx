import { type Me, type MePatch, updateMe } from "@flagnetic/api";
import { zodResolver } from "@hookform/resolvers/zod";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useMemo } from "react";
import { Controller, useForm } from "react-hook-form";
import { toast } from "sonner";
import { z } from "zod";

import { Field } from "@/components/ui/field";
import { FormPage } from "@/components/ui/form-page";
import { Input } from "@/components/ui/input";
import { Select, SelectItem } from "@/components/ui/select";
import { UnsavedChangesBar } from "@/components/ui/unsaved-changes-bar";
import { languageName } from "@/i18n/format";
import { locales } from "@/i18n/locale";
import { unwrap } from "@/lib/errors";
import { chosenLocale, followBrowser, followMemberLocale, localeChoice } from "@/lib/member-locale";
import { violationMessage } from "@/lib/problem-messages";
import { meQuery } from "@/lib/queries";

/** The server's bound on a display name, in characters. */
const displayNameMax = 200;

type ProfileMember = NonNullable<Me["member"]>;

/**
 * The member's name and console language. The email is shown but not
 * edited: it is how the instance knows the member, and an instance admin
 * changes it. A new language applies as soon as it is saved.
 */
export function ProfileForm({ member }: { member: ProfileMember }) {
  const { i18n, t } = useLingui();
  const queryClient = useQueryClient();
  const schema = z.object({
    display_name: z
      .string()
      .trim()
      .min(1, i18n._(msg`Enter a display name.`))
      .max(
        displayNameMax,
        violationMessage(i18n, {
          pointer: "/display_name",
          code: "too_long",
          detail: "",
          params: { max: displayNameMax },
        }),
      ),
    locale: z.string(),
  });
  type Values = z.infer<typeof schema>;
  // The member can change while the page is open, as when the language is
  // chosen from the personal menu: the fields not being edited follow, and
  // only the fields edited are sent, so saving never undoes such a change.
  const values = useMemo<Values>(
    () => ({ display_name: member.display_name, locale: localeChoice(member.locale) }),
    [member.display_name, member.locale],
  );
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    values,
    resetOptions: { keepDirtyValues: true },
  });
  const save = useMutation({
    mutationFn: (body: MePatch) => unwrap(updateMe({ body })),
    meta: { toast: false },
    onSuccess: async (saved: Me) => {
      queryClient.setQueryData(meQuery.queryKey, saved);
      if (saved.member) {
        form.reset({
          display_name: saved.member.display_name,
          locale: localeChoice(saved.member.locale),
        });
      }
      await followMemberLocale(i18n, saved);
      // Said after the switch, so it reads in the language just chosen.
      toast.success(i18n._(msg`Your profile is saved.`));
    },
  });
  const submit = (edited: Values) => {
    const { dirtyFields } = form.formState;
    const body: MePatch = {};
    if (dirtyFields.display_name) body.display_name = edited.display_name;
    if (dirtyFields.locale) body.locale = chosenLocale(edited.locale);
    save.mutate(body);
  };
  const { errors, isDirty } = form.formState;

  return (
    <>
      <FormPage
        onSubmit={form.handleSubmit(submit)}
        submitLabel={<Trans>Save</Trans>}
        pending={save.isPending}
        refusal={save.error}
      >
        <Field
          label={<Trans>Email</Trans>}
          hint={<Trans>How the instance knows you. An instance admin can change it.</Trans>}
        >
          <Input value={member.email} readOnly />
        </Field>
        <Field
          label={<Trans>Display name</Trans>}
          error={errors.display_name?.message}
          refusal={save.error}
          pointer="/display_name"
        >
          <Input autoComplete="name" {...form.register("display_name")} />
        </Field>
        <Controller
          control={form.control}
          name="locale"
          render={({ field }) => (
            <Field
              label={<Trans>Language</Trans>}
              hint={
                <Trans>
                  The console's language on every device you sign in on. Following the browser uses
                  the first of its languages the console offers.
                </Trans>
              }
              refusal={save.error}
              pointer="/locale"
            >
              <Select
                ref={field.ref}
                onBlur={field.onBlur}
                value={field.value}
                onValueChange={field.onChange}
              >
                <SelectItem value={followBrowser}>{t`Follow the browser`}</SelectItem>
                {locales.map((locale) => (
                  <SelectItem key={locale} value={locale} lang={locale}>
                    {languageName(locale)}
                  </SelectItem>
                ))}
              </Select>
            </Field>
          )}
        />
      </FormPage>
      <UnsavedChangesBar dirty={isDirty} onDiscard={() => form.reset()} pending={save.isPending} />
    </>
  );
}
