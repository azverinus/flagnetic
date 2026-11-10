import {
  createScimToken,
  listAuthProviderScimGroups,
  listScimTokens,
  revokeScimToken,
  type ScimGroup,
  type ScimToken,
} from "@flagnetic/api";
import { queryOptions } from "@tanstack/react-query";

import { unwrap } from "./errors";
import type { ExpiryChoice } from "./personal-tokens";

/** Every query of the instance's SCIM tokens, so a change invalidates by prefix. */
export const scimTokensKey = ["instance", "scim-tokens"] as const;

/** The instance's SCIM tokens, the newest first. */
export const scimTokensQuery = queryOptions<ScimToken[]>({
  queryKey: scimTokensKey,
  queryFn: async () => (await unwrap(listScimTokens())).items,
});

/** The groups an auth provider's directory pushed, by name. */
export function scimGroupsQuery(provider: string) {
  return queryOptions<ScimGroup[]>({
    queryKey: ["instance", "auth-providers", provider, "scim-groups"],
    queryFn: async () => (await unwrap(listAuthProviderScimGroups({ path: { provider } }))).items,
  });
}

/** The operations on SCIM tokens, through the generated client. */
export const scimTokenApi = {
  create: (body: { provider: string; expires_at: string | null }) =>
    unwrap(createScimToken({ body })),
  revoke: (token: string) => unwrap(revokeScimToken({ path: { token } })),
};

/** The expiries a SCIM token is offered: it need not expire, unlike a personal one it has no bound. */
export const scimExpiryChoices: readonly ExpiryChoice[] = ["never", "30", "90", "365"];

/** The address an identity provider is told to provision members at. */
export function scimBaseUrl(origin: string): string {
  return `${origin}/scim/v2`;
}

/** Where a SCIM token is in its life. */
export type ScimTokenState = "active" | "expired" | "revoked";

export function scimTokenState(token: ScimToken, now: Date): ScimTokenState {
  if (token.revoked_at) return "revoked";
  if (token.expires_at && new Date(token.expires_at) <= now) return "expired";
  return "active";
}
