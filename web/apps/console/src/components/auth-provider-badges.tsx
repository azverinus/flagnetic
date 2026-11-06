import type { AuthProvider } from "@flagnetic/api";
import { Trans } from "@lingui/react/macro";

import { Badge } from "@/components/ui/badge";

/** Whether the provider is offered on the sign-in page. */
export function AuthProviderStateBadge({ provider }: { provider: AuthProvider }) {
  return provider.enabled ? (
    <Badge>
      <Trans>Enabled</Trans>
    </Badge>
  ) : (
    <Badge variant="muted">
      <Trans>Disabled</Trans>
    </Badge>
  );
}

/** Which protocol the provider speaks. */
export function AuthProviderKind({ kind }: { kind: AuthProvider["kind"] }) {
  return kind === "oidc" ? <Trans>OpenID Connect</Trans> : <Trans>SAML</Trans>;
}
