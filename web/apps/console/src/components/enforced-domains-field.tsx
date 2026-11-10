import type { AuthProvider, SsoEnforcedDomain } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { PlusIcon, XIcon } from "lucide-react";
import { useId } from "react";

import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Select, SelectItem } from "@/components/ui/select";
import { enforceableDomains, providersVouchingFor } from "@/lib/auth-settings";

/** What is wrong with one row, in the member's language. */
export type EnforcedRowErrors = { domain?: string; provider?: string };

export type EnforcedDomainsFieldProps = {
  value: SsoEnforcedDomain[];
  onChange: (value: SsoEnforcedDomain[]) => void;
  providers: readonly AuthProvider[];
  /** The errors per row, by the row's index. */
  errors: readonly (EnforcedRowErrors | undefined)[];
};

/**
 * The email domains sent to a provider. Each row is a domain and the
 * provider its people sign in through; the domain is chosen among those
 * the enabled providers vouch for, and the provider among those that
 * vouch for the domain, because the server refuses any other pairing.
 */
export function EnforcedDomainsField({
  value,
  onChange,
  providers,
  errors,
}: EnforcedDomainsFieldProps) {
  const { t } = useLingui();
  const id = useId();
  const domains = enforceableDomains(providers, value);
  const nameOf = (slug: string) =>
    providers.find((provider) => provider.slug === slug)?.display_name ?? slug;

  const update = (index: number, row: SsoEnforcedDomain) =>
    onChange(value.map((existing, i) => (i === index ? row : existing)));

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <Label id={`${id}-label`}>
          <Trans>Enforced domains</Trans>
        </Label>
        <p id={`${id}-hint`} className="text-sm text-muted-foreground">
          <Trans>
            Everyone with an email address in an enforced domain signs in through its provider only:
            no password, no other provider, and the sign-in page sends the address to the provider.
            A domain is offered once an enabled auth provider lists it among its domains. Instance
            admins are not exempt.
          </Trans>
        </p>
      </div>
      {value.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          <Trans>No domain is enforced.</Trans>
        </p>
      ) : (
        <ul aria-labelledby={`${id}-label`} className="flex flex-col gap-3">
          {value.map((row, index) => {
            const number = index + 1;
            const vouching = providersVouchingFor(providers, row.domain);
            // A provider the row holds stays shown though it no longer
            // vouches (disabled, say), so its refusal can be read.
            const offered =
              row.provider !== "" && !vouching.some((provider) => provider.slug === row.provider)
                ? [...vouching, { slug: row.provider, display_name: nameOf(row.provider) }]
                : vouching;
            const rowErrors = errors[index];
            const domainErrorId = `${id}-${index}-domain-error`;
            const providerErrorId = `${id}-${index}-provider-error`;
            return (
              <li key={index} className="flex items-start gap-2">
                <div className="flex flex-1 flex-col gap-1">
                  <Select
                    aria-label={t`Domain ${number}`}
                    aria-invalid={rowErrors?.domain ? true : undefined}
                    aria-describedby={rowErrors?.domain ? domainErrorId : undefined}
                    placeholder={t`Choose a domain`}
                    value={row.domain}
                    onValueChange={(domain) => {
                      const stillVouches = providersVouchingFor(providers, domain).some(
                        (provider) => provider.slug === row.provider,
                      );
                      update(index, { domain, provider: stillVouches ? row.provider : "" });
                    }}
                  >
                    {domains.map((domain) => (
                      <SelectItem key={domain} value={domain}>
                        {domain}
                      </SelectItem>
                    ))}
                  </Select>
                  {rowErrors?.domain ? (
                    <p id={domainErrorId} className="text-sm text-destructive">
                      {rowErrors.domain}
                    </p>
                  ) : null}
                </div>
                <div className="flex flex-1 flex-col gap-1">
                  <Select
                    aria-label={t`Provider ${number}`}
                    aria-invalid={rowErrors?.provider ? true : undefined}
                    aria-describedby={rowErrors?.provider ? providerErrorId : undefined}
                    placeholder={t`Choose a provider`}
                    value={row.provider}
                    disabled={row.domain === ""}
                    onValueChange={(provider) => update(index, { ...row, provider })}
                  >
                    {offered.map((provider) => (
                      <SelectItem key={provider.slug} value={provider.slug}>
                        {provider.display_name}
                      </SelectItem>
                    ))}
                  </Select>
                  {rowErrors?.provider ? (
                    <p id={providerErrorId} className="text-sm text-destructive">
                      {rowErrors.provider}
                    </p>
                  ) : null}
                </div>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  aria-label={t`Remove enforced domain ${number}`}
                  onClick={() => onChange(value.filter((_, i) => i !== index))}
                >
                  <XIcon aria-hidden />
                </Button>
              </li>
            );
          })}
        </ul>
      )}
      <div>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => onChange([...value, { domain: "", provider: "" }])}
        >
          <PlusIcon aria-hidden />
          <Trans>Add enforced domain</Trans>
        </Button>
      </div>
    </div>
  );
}
