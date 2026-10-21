import type { Role, Scope } from "@flagnetic/api";
import { zodResolver } from "@hookform/resolvers/zod";
import { Trans, useLingui } from "@lingui/react/macro";
import type { QueryKey } from "@tanstack/react-query";
import { useEffect, useId, useMemo } from "react";
import { Controller, useForm, useWatch } from "react-hook-form";
import { z } from "zod";

import { RoleField } from "@/components/role-field";
import { DialogForm } from "@/components/ui/dialog-form";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select, SelectItem } from "@/components/ui/select";
import { formatLifetime } from "@/i18n/format";
import { ApiError } from "@/lib/errors";
import { nameSchema } from "@/lib/forms";
import { useListMutation } from "@/lib/mutations";
import {
  defaultExpiry,
  type ExpiryChoice,
  expiresAt,
  expiryChoices,
  impliesRead,
  scopeDescription,
  type TokenPolicy,
  withImpliedRead,
} from "@/lib/personal-tokens";
import { violationsByPointer } from "@/lib/problem-messages";

/** What a new token of either kind is created with. */
export type TokenRequest = {
  name: string;
  scopes: Scope[];
  expires_at: string | null;
};

/** A token created, with its value, to be shown this once. */
type Created = { value: string };

/**
 * The kind of token the form creates: a personal access token acts for
 * its member, a project token holds one of the project's roles.
 */
export type TokenKind =
  | {
      kind: "personal";
      /** Creates the token; the answer carries its value. */
      create: (request: TokenRequest) => Promise<Created>;
    }
  | {
      kind: "project";
      /** Every role, in the server's order; the token holds one of them. */
      roles: readonly Role[];
      /** Creates the token holding the chosen role; the answer carries its value. */
      create: (request: TokenRequest & { role: string }) => Promise<Created>;
    };

export type TokenCreateFormProps = TokenKind & {
  /** The scopes the token may be given, in the server's order. */
  scopes: readonly Scope[];
  /** The instance's bound on a token's expiry, which the expiries offered follow. */
  policy: TokenPolicy;
  /** The list the token joins, read again once it is created. */
  list: QueryKey;
  onCancel: () => void;
  /** The token created, with its value, to be shown this once. */
  onCreated: (created: Created) => void;
};

/**
 * A new API token: its name, for a project token its role, its scopes and
 * its expiry, offered only as far ahead as the instance allows.
 */
export function TokenCreateForm(props: TokenCreateFormProps) {
  const { scopes, policy, list, onCancel, onCreated } = props;
  const roles = props.kind === "project" ? props.roles : undefined;
  const { i18n, t } = useLingui();
  const { maxLifetimeSeconds: max, expiryRequired } = policy;
  const choices = useMemo(
    () => expiryChoices({ maxLifetimeSeconds: max, expiryRequired }),
    [max, expiryRequired],
  );
  const scopesHint = useId();

  const schema = z.object({
    name: nameSchema(i18n),
    // Nothing is chosen first, so a project token's role is always picked on purpose.
    role: props.kind === "project" ? z.string().min(1, t`Choose a role.`) : z.string(),
    scopes: z.array(z.string()),
    expiry: z.string(),
  });
  type Values = z.infer<typeof schema>;
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { name: "", role: "", scopes: ["read"], expiry: defaultExpiry(choices) },
  });
  // The policy can change while the form is open, when the caller is read
  // again; an expiry it no longer offers would show as nothing chosen.
  const expiry = useWatch({ control: form.control, name: "expiry" });
  useEffect(() => {
    if (!choices.includes(expiry as ExpiryChoice)) form.setValue("expiry", defaultExpiry(choices));
  }, [choices, expiry, form]);
  const create = useListMutation(
    (values: Values) => {
      const request: TokenRequest = {
        name: values.name,
        scopes: withImpliedRead(values.scopes as Scope[]),
        expires_at: expiresAt(values.expiry as ExpiryChoice, new Date(), policy),
      };
      return props.kind === "project"
        ? props.create({ ...request, role: values.role })
        : props.create(request);
    },
    list,
    () => undefined,
    // The answer carries the token's value.
    { secret: true },
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
      {roles ? (
        <Controller
          control={form.control}
          name="role"
          render={({ field }) => (
            <RoleField
              roles={roles}
              value={field.value}
              onChange={field.onChange}
              onBlur={field.onBlur}
              ref={field.ref}
              error={errors.role?.message}
              refusal={create.error}
            />
          )}
        />
      ) : null}
      <Controller
        control={form.control}
        name="scopes"
        render={({ field }) => (
          <fieldset aria-describedby={scopesHint} className="flex flex-col gap-2">
            <legend className="text-sm font-medium">
              <Trans>Scopes</Trans>
            </legend>
            <p id={scopesHint} className="text-sm text-muted-foreground">
              {props.kind === "project" ? (
                <Trans>
                  A project token does what its role allows in this project, narrowed to its scopes,
                  and never manages who has access. Every write scope includes read.
                </Trans>
              ) : (
                <Trans>
                  A token does only what you can do yourself, narrowed to its scopes. Every write
                  scope includes read.
                </Trans>
              )}
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
