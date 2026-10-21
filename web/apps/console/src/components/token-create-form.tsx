import { type CreatedApiToken, createMyToken, type Scope } from "@flagnetic/api";
import { zodResolver } from "@hookform/resolvers/zod";
import { Trans, useLingui } from "@lingui/react/macro";
import { useEffect, useId, useMemo } from "react";
import { Controller, useForm, useWatch } from "react-hook-form";
import { z } from "zod";

import { DialogForm } from "@/components/ui/dialog-form";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select, SelectItem } from "@/components/ui/select";
import { formatLifetime } from "@/i18n/format";
import { ApiError, unwrap } from "@/lib/errors";
import { nameSchema } from "@/lib/forms";
import { useListMutation } from "@/lib/mutations";
import {
  defaultExpiry,
  type ExpiryChoice,
  expiresAt,
  expiryChoices,
  impliesRead,
  offeredScopes,
  scopeDescription,
  type TokenPolicy,
  withImpliedRead,
} from "@/lib/personal-tokens";
import { violationsByPointer } from "@/lib/problem-messages";
import { myTokensQuery } from "@/lib/queries";

export type TokenCreateFormProps = {
  /** Whether the member is an instance admin, who alone may give a token `instance:admin`. */
  instanceAdmin: boolean;
  /** The instance's bound on a token's expiry, which the expiries offered follow. */
  policy: TokenPolicy;
  onCancel: () => void;
  /** The token created, with its value, to be shown this once. */
  onCreated: (created: CreatedApiToken) => void;
};

/**
 * A new personal access token: its name, its scopes and its expiry, offered
 * only as far ahead as the instance allows.
 */
export function TokenCreateForm({
  instanceAdmin,
  policy,
  onCancel,
  onCreated,
}: TokenCreateFormProps) {
  const { i18n, t } = useLingui();
  const { maxLifetimeSeconds: max, expiryRequired } = policy;
  const choices = useMemo(
    () => expiryChoices({ maxLifetimeSeconds: max, expiryRequired }),
    [max, expiryRequired],
  );
  const scopes = offeredScopes(instanceAdmin);
  const scopesHint = useId();

  const schema = z.object({
    name: nameSchema(i18n),
    scopes: z.array(z.string()),
    expiry: z.string(),
  });
  type Values = z.infer<typeof schema>;
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { name: "", scopes: ["read"], expiry: defaultExpiry(choices) },
  });
  // The policy can change while the form is open, when the caller is read
  // again; an expiry it no longer offers would show as nothing chosen.
  const expiry = useWatch({ control: form.control, name: "expiry" });
  useEffect(() => {
    if (!choices.includes(expiry as ExpiryChoice)) form.setValue("expiry", defaultExpiry(choices));
  }, [choices, expiry, form]);
  const create = useListMutation(
    (values: Values) =>
      unwrap(
        createMyToken({
          body: {
            name: values.name,
            scopes: withImpliedRead(values.scopes as Scope[]),
            expires_at: expiresAt(values.expiry as ExpiryChoice, new Date(), policy),
          },
        }),
      ),
    myTokensQuery.queryKey,
    () => undefined,
  );
  const { errors } = form.formState;
  // A refused scope is named by its index in the list sent: shown under the list.
  const scopeRefused =
    create.error instanceof ApiError
      ? [...violationsByPointer(i18n, create.error).entries()].find(([pointer]) =>
          pointer.startsWith("/scopes"),
        )?.[1]
      : undefined;
  const chosen = useWatch({ control: form.control, name: "scopes" });
  const anyWrite = chosen.some((scope) => impliesRead(scope as Scope));
  const days = (n: number) => formatLifetime(i18n, n * 86_400);
  const expiryName: Record<ExpiryChoice, string> = {
    "7": days(7),
    "30": days(30),
    "90": days(90),
    "365": t`A year`,
    never: t`Never`,
  };
  const lifetime = max === undefined ? "" : formatLifetime(i18n, max);

  return (
    <DialogForm
      onSubmit={form.handleSubmit((values) => create.mutate(values, { onSuccess: onCreated }))}
      onCancel={onCancel}
      submitLabel={<Trans>Create token</Trans>}
      pending={create.isPending}
      refusal={create.error}
    >
      <Field
        label={<Trans>Name</Trans>}
        hint={<Trans>What uses the token, to tell it apart later.</Trans>}
        error={errors.name?.message}
        refusal={create.error}
        pointer="/name"
      >
        <Input autoComplete="off" autoFocus {...form.register("name")} />
      </Field>
      <Controller
        control={form.control}
        name="scopes"
        render={({ field }) => (
          <fieldset aria-describedby={scopesHint} className="flex flex-col gap-2">
            <legend className="text-sm font-medium">
              <Trans>Scopes</Trans>
            </legend>
            <p id={scopesHint} className="text-sm text-muted-foreground">
              <Trans>
                A token does only what you can do yourself, narrowed to its scopes. Every write
                scope includes read.
              </Trans>
            </p>
            {scopes.map((scope) => {
              const implied = scope === "read" && anyWrite;
              const checked = implied || field.value.includes(scope);
              return (
                <label key={scope} className="flex items-start gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={checked}
                    disabled={implied}
                    onChange={(event) =>
                      field.onChange(
                        event.target.checked
                          ? [...field.value, scope]
                          : field.value.filter((s) => s !== scope),
                      )
                    }
                    className="mt-0.5 size-4 accent-primary"
                  />
                  <span className="flex flex-col">
                    <code className="font-mono text-xs">{scope}</code>
                    <span className="text-muted-foreground">{scopeDescription(i18n, scope)}</span>
                  </span>
                </label>
              );
            })}
            {scopeRefused ? <p className="text-sm text-destructive">{scopeRefused}</p> : null}
          </fieldset>
        )}
      />
      <Controller
        control={form.control}
        name="expiry"
        render={({ field }) => (
          <Field
            label={<Trans>Expires</Trans>}
            hint={
              max === undefined ? (
                expiryRequired ? (
                  <Trans>This instance requires every token to expire.</Trans>
                ) : undefined
              ) : expiryRequired ? (
                <Trans>
                  This instance allows a token at most {lifetime} ahead, and every token must
                  expire.
                </Trans>
              ) : (
                <Trans>This instance allows a token at most {lifetime} ahead.</Trans>
              )
            }
            refusal={create.error}
            pointer="/expires_at"
          >
            <Select
              ref={field.ref}
              onBlur={field.onBlur}
              value={field.value}
              onValueChange={field.onChange}
            >
              {choices.map((choice) => (
                <SelectItem key={choice} value={choice}>
                  {expiryName[choice]}
                </SelectItem>
              ))}
            </Select>
          </Field>
        )}
      />
    </DialogForm>
  );
}
