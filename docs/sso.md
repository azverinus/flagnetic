# Single sign-on

Members sign in through an identity provider the instance admin has added as an [auth provider](access.md#auth-providers). This guide covers OpenID Connect: how to set a provider up, what a sign-in does, how a person is matched to a member, and what each error on the sign-in page means.

## Setting up an OIDC provider

1. Set `FLAGNETIC_BASE_URL` to the address members reach the console at, such as `https://flags.example.com`. Without it a provider cannot be created, edited or enabled, and the redirect URI below cannot be built.
2. In the identity provider, register a client for a web application that uses the authorization code flow, and give it the redirect URI the instance computes: `<base URL>/sso/<slug>/callback`, answered as `redirect_uri` when the provider is created. Take note of the client ID and the client secret. The instance sends the client secret to the token endpoint and uses PKCE besides, so a client that insists on PKCE alone is fine too.
3. As an instance admin, add the provider with `POST /instance/auth-providers`: a `slug` of your choice, the `issuer` (the address under which the identity provider serves `/.well-known/openid-configuration`), the `client_id`, the `client_secret`, and the `domains` the provider vouches for.
4. Check it with `POST /instance/auth-providers/{slug}/test`, which fetches the discovery document and says what it found.

The provider now appears as a button on the sign-in page, and `GET /sso/<slug>/start` begins a sign-in.

The issuer, and every endpoint its discovery document names, must be `https`, except on a loopback address. Private networks are allowed.

### Domains, linking and provisioning

A provider vouches only for the email domains it names. A domain is the part of the address after the `@`, compared whatever its case; a subdomain is not its parent (`mail.example.com` is not `example.com`).

- A person whose identity is already linked signs in whatever their email says: the link is the proof.
- Otherwise the email must be in one of the provider's domains, and verified (the provider's `email_verified` claim is exactly `true`) unless `require_verified_email` is switched off.
- With `link_by_email` on, an **active** member holding the email is then linked to the person and signed in. With it off, they are not found.
- With `auto_provision` on, an email no member holds creates a new **active** member, named after the `name` claim or else the email, with no password.
- Anything else is `member_not_found`. A member who was deactivated is `member_deactivated`, whichever rule would have matched them.
- An invited member is not signed in by a provider yet: they accept their invitation.

If the ID token carries no email, the instance asks the provider's UserInfo endpoint for it, and trusts the answer only when its `sub` is the token's; its `email_verified` is judged on its own.

### Example: Dex

```yaml
staticClients:
  - id: flagnetic
    secret: change-me
    name: Flagnetic
    redirectURIs:
      - https://flags.example.com/sso/dex/callback
```

Add a provider with slug `dex`, issuer `https://dex.example.com`, client ID `flagnetic`, that client secret and `domains: ["example.com"]`.

### Example: Keycloak

Create an OpenID Connect client in the realm, with **Client authentication** on and the standard flow enabled. Set **Valid redirect URIs** to `https://flags.example.com/sso/keycloak/callback`. The issuer is `https://keycloak.example.com/realms/<realm>`; the client secret is on the client's *Credentials* tab. Keycloak sets `email_verified` from the user's *Email verified* switch.

## What a sign-in does

1. The browser opens `/sso/<slug>/start?return_to=<path>`. The instance creates a sign-in attempt that lives ten minutes, sets the cookie `__Host-flagnetic_sso` (plain `flagnetic_sso` only where cookies cannot be `Secure`: on `localhost`, or when `FLAGNETIC_INSECURE_COOKIES` allows it) and sends the browser to the identity provider with a state, a nonce and a PKCE S256 challenge.
2. The identity provider sends the browser back to `/sso/<slug>/callback`. The instance exchanges the code, verifies the ID token (signature, issuer, audience, expiry, nonce), and stores the verified claims on the attempt. **Nothing about any member changes yet.** An answer is taken once: a replayed one finds the attempt used.
3. The browser is redirected to `/sso/complete/<attempt>`. The instance checks that the browser presents the attempt's cookie, in constant time, then, in one transaction, resolves the person to a member, provisions or links as the provider allows, opens the session and redirects to `return_to`. A completion from any other browser changes nothing: no member, no identity, no session.

The session records the provider it opened through; `GET /me/sessions` answers `method: oidc` and the provider's slug.

`return_to` is honoured only when it is a path on the instance: one leading `/`, no backslash, no control or whitespace character, no scheme or host. Anything else, such as `//evil.example` or `https://evil.example`, is ignored and the sign-in lands on `/`.

An unknown or disabled provider answers `404` on all three addresses. Disabling a provider keeps its members, identities and grants and stops its sign-ins.

A provider with identities cannot be deleted (`409 provider_in_use`, `used_by: ["identities"]`); disable it instead.

## Sign-in errors

Every failure redirects to `/sign-in?error=<code>`:

| Code | Meaning | What to do |
|---|---|---|
| `sso_failed` | The sign-in could not be verified: the state was unknown, used or expired, the token's signature, issuer, audience, expiry or nonce was wrong, the code exchange was refused (check the client secret), the provider could not be reached, or the attempt was gone when it was completed. | Try again. If it repeats, `POST …/test` the provider and read the server's log: a record `single sign-on failed` carries the cause. |
| `idp_error` | The identity provider refused the sign-in itself, for example the person denied access. | Ask the identity provider's administrator. |
| `member_not_found` | No member matches the person: their email is outside the provider's domains or unverified, linking by email is off, provisioning is off, or they hold an invitation they have not accepted. | Add the domain, switch `link_by_email` or `auto_provision` on, or invite the person. |
| `member_deactivated` | The member was deactivated. The failure is audited as `auth.sign_in_failed`. | An instance admin may reactivate them. |
| `attempt_mismatch` | The browser completing the sign-in is not the one that started it. | Start again in the same browser. |

## Audit and metrics

A first sign-in records `member.provisioned` or `identity.linked` (or both), and every sign-in `auth.signed_in` with its `method` (`oidc`) and `provider`; a refused sign-in of a known member (one the subject is linked to, or one holding an email the provider vouches for) records `auth.sign_in_failed` with the `reason`: the member's status when it refuses them (`deactivated`, `invited`), or `not_linked` for an active member when the provider does not link by email.

`flagnetic_sso_sign_ins_total{kind,result}` counts sign-ins that ended, by `kind` (`oidc`) and `result`: `succeeded` or the code above.
