import type { AuthProvider, AuthProviderKind } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useState } from "react";

import { AuthProviderForm } from "@/components/auth-provider-form";
import { SamlProviderForm } from "@/components/saml-provider-form";

export type NewAuthProviderProps = {
  onCancel: () => void;
  /** The provider as it was created. */
  onSaved: (saved: AuthProvider) => void;
};

/**
 * A new auth provider, of the protocol the identity provider speaks: the
 * choice comes first, and the form below asks what that protocol needs.
 * The choice sits outside the form, so changing it keeps the focus on it.
 */
export function NewAuthProvider({ onCancel, onSaved }: NewAuthProviderProps) {
  const { t } = useLingui();
  const [kind, setKind] = useState<AuthProviderKind>("oidc");
  // The form takes the focus when the dialog opens, and leaves it on the
  // choice once the member changes the protocol.
  const [chosen, setChosen] = useState(false);
  return (
    <div className="flex flex-col gap-5">
      <fieldset className="flex flex-col gap-2">
        <legend className="text-sm font-medium">
          <Trans>Protocol</Trans>
        </legend>
        <div className="flex flex-wrap gap-x-6 gap-y-2">
          {(
            [
              ["oidc", t`OpenID Connect`],
              ["saml", t`SAML`],
            ] as const satisfies readonly [AuthProviderKind, string][]
          ).map(([value, label]) => (
            <label key={value} className="flex items-center gap-2 text-sm">
              <input
                type="radio"
                name="auth-provider-kind"
                value={value}
                checked={kind === value}
                onChange={() => {
                  setKind(value);
                  setChosen(true);
                }}
                className="size-4 accent-primary"
              />
              {label}
            </label>
          ))}
        </div>
      </fieldset>
      {kind === "oidc" ? (
        <AuthProviderForm layout="dialog" autoFocus={!chosen} onCancel={onCancel} onSaved={onSaved} />
      ) : (
        <SamlProviderForm layout="dialog" autoFocus={!chosen} onCancel={onCancel} onSaved={onSaved} />
      )}
    </div>
  );
}
