# Single sign-on

Members sign in through an identity provider the instance admin has added as an [auth provider](access.md#auth-providers). This guide covers OpenID Connect and SAML: how to set a provider up, what a sign-in does, how a person is matched to a member, and what each error on the sign-in page means.

## Setting up an OIDC provider

1. Set `FLAGNETIC_BASE_URL` to the address members reach the console at, such as `https://flags.example.com`. Without it a provider cannot be created, edited or enabled, and the redirect URI below cannot be built.
2. In the identity provider, register a client for a web application that uses the authorization code flow, and give it the redirect URI the instance computes: `<base URL>/sso/<slug>/callback`, answered as `redirect_uri` when the provider is created. Take note of the client ID and the client secret. The instance sends the client secret to the token endpoint and uses PKCE besides, so a client that insists on PKCE alone is fine too.
3. As an instance admin, add the provider under **Auth providers** in the console (**New provider**; its page shows the redirect URI to copy), or with `POST /instance/auth-providers`: a `slug` of your choice, the `issuer` (the address under which the identity provider serves `/.well-known/openid-configuration`), the `client_id`, the `client_secret`, and the `domains` the provider vouches for.
4. Check it with **Test** on the provider's page, or `POST /instance/auth-providers/{slug}/test`, which fetches the discovery document and says what it found. The console also warns when the document does not list the `S256` PKCE method, which sign-in needs.

The provider now appears as a button on the sign-in page, and `GET /sso/<slug>/start` begins a sign-in.

The issuer, and every endpoint its discovery document names, must be `https`, except on a loopback address. Private networks are allowed.

### Domains, linking and provisioning

A provider vouches only for the email domains it names. A domain is the part of the address after the `@`, compared whatever its case; a subdomain is not its parent (`mail.example.com` is not `example.com`).

- A person whose identity is already linked signs in whatever their email says: the link is the proof.
- So does a person whose subject is the `externalId` of a member the provider [provisioned through SCIM](scim.md#signing-in): the directory named them itself, so the sign-in is linked to that member, at this provider only, with `link_by_email` and `auto_provision` off.
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

## Setting up a SAML provider

1. Set `FLAGNETIC_BASE_URL`, as for an OIDC provider.
2. As an instance admin, add the provider with `POST /instance/auth-providers`: a `slug`, `kind: saml`, the `domains` it vouches for, and the identity provider's metadata, either as `idp_metadata_url` (fetched now) or pasted as `idp_metadata_xml`; give one of the two. The answer carries the three addresses to register at the identity provider:
   - `entity_id`, the instance's entity ID for this provider (the audience of its assertions): `<base URL>/sso/<slug>/metadata`;
   - `metadata_url`, where the instance serves its SAML metadata, which is the same address (`GET /sso/<slug>/metadata`, `application/samlmetadata+xml`), for an identity provider that imports metadata from a URL;
   - `acs_url`, the assertion consumer service the identity provider posts its responses to: `<base URL>/sso/<slug>/acs`.
3. Register the instance at the identity provider with those addresses, or by importing the metadata. Several identity providers insist on the entity ID and the ACS being entered by hand; they must be exactly as answered.
4. Check it with `POST /instance/auth-providers/{slug}/test`, which reads the identity provider's metadata again (fetching it when it came from a URL) and answers what it found: `idp_entity_id`, `idp_sso_url` and the `idp_certificates` (their `sha256` and `not_after`). Nothing is stored.

The provider appears as a button on the sign-in page, and `GET /sso/<slug>/start` begins a sign-in.

### The identity provider's metadata

The metadata is read when the provider is created and again whenever an edit names `idp_metadata_url` or `idp_metadata_xml`; naming one clears the other. The instance takes the entity ID, the single sign-on endpoint with the HTTP-Redirect binding and the signing certificates from it.

- A metadata URL must be `https`, except on a loopback address (`422 insecure_url`); the document is at most 1 MiB, and one that cannot be fetched is `502 provider_unreachable` with the `failure`.
- Metadata that cannot be used is `422 invalid_auth_provider` with a violation at the field it came from: `invalid_metadata` (not an EntityDescriptor of an identity provider, a DOCTYPE, a certificate that cannot be read, an http single sign-on address away from loopback), `no_redirect_binding` (the identity provider offers no HTTP-Redirect single sign-on service) or `no_signing_certificate`.
- The `idp_certificates` are the certificates a response may be signed with. The instance does not refresh them yet: to follow the identity provider's rotation, edit the provider with the new metadata.

### The instance's key pair

Creating a SAML provider generates a key pair for it: an RSA-2048 key and a self-signed certificate valid for three years. The private key is sealed under the instance's secret key and never leaves the instance; the certificate is in the SP metadata. The instance signs its authentication requests with the key (`sign_requests`, default true: the HTTP-Redirect binding signs the query with RSA-SHA256) and decrypts encrypted assertions with it; an identity provider that encrypts assertions needs the certificate from the SP metadata.

### Attributes, name identifiers and signing

- `claims` names the assertion attributes the instance reads, as for an OIDC provider's claims: `email`, `name` and `groups` by default. An attribute is matched by its `Name` or its `FriendlyName`, and its first value counts. The email is the NameID when its format is `emailAddress`, otherwise the attribute named by `claims.email`.
- The subject the sign-in links to an identity is the NameID. With a `transient` NameID it changes at each sign-in, so nothing stays linked: ask for `persistent`, `emailAddress` or `unspecified` instead.
- `name_id_format` (`unspecified`, the default, `emailAddress`, `persistent` or `transient`) is the format the request asks for.
- A SAML provider has no verification flag to read: the instance counts an email as verified only inside the provider's `domains`, which are also what decide linking by email and provisioning.
- Fields of the other kind are refused: `issuer`, `client_id`, `client_secret`, `scopes` and `require_verified_email` belong to OIDC providers, `idp_metadata_url`, `idp_metadata_xml`, `sign_requests` and `name_id_format` to SAML ones (`422 invalid_auth_provider`, violation `not_for_kind`). The pasted document is never answered.

### What the instance checks of a response

The identity provider posts a SAML response to the ACS (at most 1 MiB; the address takes the cross-site POST an identity provider makes, and is bound by the attempt it answers instead). It is verified with [gosaml2](https://github.com/russellhaering/gosaml2) and refused unless:

- the Response or the Assertion is signed by one of the provider's certificates, and every claim is read from the element the signature covers: an assertion wrapped beside a signed one, or an unsigned one, is not read;
- it is from the identity provider's entity, addressed to the ACS (`Destination` and `Recipient`) and to the instance's entity ID (`Audience`), and its status is success;
- it answers the request the attempt made: `InResponseTo` is the request's ID. A response nobody asked for is refused, so a sign-in cannot start at the identity provider;
- its conditions and its subject confirmation hold, with two minutes of clock skew;
- it holds one assertion, and that assertion was not accepted before: each is remembered until it could no longer be accepted;
- an encrypted assertion uses RSA-OAEP key transport and AES-CBC or AES-GCM; `rsa-1_5` is refused, and so is any document with a DOCTYPE.

A response that does not pass is `sso_failed`, and a response that says the identity provider refused the person is `idp_error`. The server's log record `single sign-on failed` carries the cause.

An identity provider that starts the sign-in itself (IdP-initiated sign-in) and single logout are not offered.

### Example: Keycloak

Create a client of type SAML in the realm. **Client ID** is the `entity_id`; **Valid redirect URIs** and **Master SAML Processing URL** (or *Assertion Consumer Service POST Binding URL*) the `acs_url`. Turn on **Sign assertions**, set **Name ID format** to `username`, `email` or `persistent`, and add *User Property* mappers `email` (property `email`) and `name`, and a *Group list* mapper named `groups`. Keycloak does not need to verify the instance's requests, so **Client signature required** may stay off; to have it check them, upload the certificate of the SP metadata. The identity provider's metadata is `https://keycloak.example.com/realms/<realm>/protocol/saml/descriptor`.

### Example: Entra ID

In *Enterprise applications*, create an application and choose *Single sign-on*, *SAML*. Set **Identifier (Entity ID)** to the `entity_id` and **Reply URL (Assertion Consumer Service URL)** to the `acs_url`. Under *Attributes & claims*, set the **Unique User Identifier** (NameID) to `user.mail` with the format *Email address*, so the NameID is the email. The attribute names Entra ID sends are URIs; to read the name and the groups, give them in `claims`:

- `name`: `http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name`;
- `groups`: `http://schemas.microsoft.com/ws/2008/06/identity/claims/groups` (add a groups claim first).

Entra ID signs the assertion by default. Use the *App Federation Metadata Url* as `idp_metadata_url`. To have Entra ID encrypt assertions, upload the certificate of the SP metadata as a token encryption certificate.

### Example: Okta

Create a *SAML 2.0* app integration. Set **Single sign-on URL** to the `acs_url` and **Audience URI (SP Entity ID)** to the `entity_id`, **Name ID format** to `EmailAddress` and **Application username** to `Email`. Add attribute statements `email` (`user.email`), `name` (`user.displayName`) and, for groups, `groups` with a filter. Set `idp_metadata_url` to the *Metadata URL* of the *Sign On* tab. Okta signs the assertion; to have it encrypt, switch on **Assertion Encryption** with the certificate of the SP metadata.

## Group mappings

A provider's group mappings turn the directory groups of the people who sign in through it into access: a role in a project, or instance admin. The access follows the directory: it is made when a person signs in with a group, and removed when they no longer have it.

**Reading the groups.** The identity provider has to send them. For an OIDC provider that is the claim `claims.groups` names (default `groups`) in the ID token, and the provider usually needs a scope or a setting to include it: Dex and Keycloak ask for the `groups` scope, Okta for a groups claim on the authorization server, Entra ID for the groups claim of the app registration. For a SAML provider it is the multi-valued attribute `claims.groups` names. A claim counts only when it is a list of strings; a list may be empty, which says the person is in no group.

**Mapping groups.** `PUT /instance/auth-providers/{slug}/group-mappings` replaces the provider's whole list, and `GET` reads it. An instance admin's call, with a body of `items`, in order:

```json
{
  "items": [
    { "group": "engineering", "project": "checkout", "role": "developer" },
    { "group": "platform-leads", "project": "checkout", "role": "owner" },
    { "group": "it-admins", "instance_admin": true }
  ]
}
```

A mapping names a `group`, matched exactly as the identity provider spells it, and either a `project` (its key) with one of the built-in roles `owner`, `maintainer`, `developer` and `viewer`, or `instance_admin` alone. A blank group, a mapping that is both or neither, an unknown role and a project that does not exist are refused `422 invalid_group_mappings`, naming each field, and nothing changes. Several groups may map to the same role, and one group to several projects; a person holds the union of what their groups map to.

**What a sign-in does.** Completing a sign-in stores the groups the person claimed, then makes the grants and the instance admin their groups map to and removes the provider's others. A person who loses a group loses what only that group gave them, at their next sign-in. A sign-in whose claim is missing or is not a list of strings changes nothing: the person keeps what the provider granted, and the server logs `the groups claim is missing or is not a list of strings` when the provider has mappings, so a misconfigured identity provider cannot strip everybody of their access.

**Replacing the mappings runs them at once.** The new list is run in the same transaction for every member who has signed in through the provider, from the groups they last claimed, so a mapping you remove stops granting immediately instead of at each member's next sign-in. The answer counts what happened:

```json
{ "granted": 12, "revoked": 3, "kept_instance_admin": 0 }
```

`granted` counts the grants made and the members made instance admin, `revoked` the grants removed and the members no longer instance admin, `kept_instance_admin` the members the run could not demote because they are the last active instance admin. A deactivated member keeps none of the provider's grants and gains none.

**Each source owns its own grants.** A grant a mapping made has `source` `sso` and names the provider; a grant made by hand has `source` `manual`. They sit side by side: a mapping never skips a project because the member holds a role there by hand, a hand grant may be added beside a mapped one, and two providers may each grant the same role. A provider replaces and removes only its own.

- A mapped grant cannot be removed by hand: `DELETE /projects/{project}/grants/{grant}` answers `409 sso_sourced`. Change the mapping or the directory instead.
- An instance admin a mapping made cannot be demoted by hand: `PATCH /members/{member}` with `instance_admin` false answers `409 sso_sourced`. Only the provider that made them takes it away. An instance admin made by hand is never taken away by a provider, and a provider never demotes the last active instance admin: that is logged and counted in `kept_instance_admin`, and neither fails the sign-in nor the replacement.
- A member who loses `grant.manage` in a project through a mapping loses, as through any other change, the grant of each invitation they issued to it (`invitation.grant_revoked`).
- A provider whose mappings made a grant or an instance admin cannot be deleted (`409 provider_in_use`, `used_by` naming `grants`): disable it instead.

Replacing the mappings is audited as `group_mappings.updated` (the list before and after, and the counts) by the instance admin who did it. Every grant, revocation, promotion and demotion a mapping makes is audited as `grant.created`, `grant.deleted`, `member.promoted` or `member.demoted` with the provider as the actor (kind `sso`, named by its slug), and carries the `source` and the `provider`.

## What a sign-in does

1. The browser opens `/sso/<slug>/start?return_to=<path>`. The instance creates a sign-in attempt that lives ten minutes, sets the cookie `__Host-flagnetic_sso` (plain `flagnetic_sso` only where cookies cannot be `Secure`: on `localhost`, or when `FLAGNETIC_INSECURE_COOKIES` allows it) and sends the browser to the identity provider with a state, a nonce and a PKCE S256 challenge.
2. The identity provider sends the browser back to `/sso/<slug>/callback`. The instance exchanges the code, verifies the ID token (signature, issuer, audience, expiry, nonce), and stores the verified claims on the attempt. **Nothing about any member changes yet.** An answer is taken once: a replayed one finds the attempt used.
3. The browser is redirected to `/sso/complete/<attempt>`. The instance checks that the browser presents the attempt's cookie, in constant time, then, in one transaction, resolves the person to a member, provisions or links as the provider allows, gives the member what their groups map to ([group mappings](#group-mappings)), opens the session and redirects to `return_to`. A completion from any other browser changes nothing: no member, no identity, no session.

A SAML sign-in differs in its first two steps. The instance sends the browser to the identity provider's single sign-on endpoint with an AuthnRequest, by the HTTP-Redirect binding, whose ID the attempt remembers and whose `RelayState` is the attempt's state; the identity provider posts its response to `/sso/<slug>/acs`, where the instance verifies it as [above](#what-the-instance-checks-of-a-response) and stores the claims on the attempt. The attempt is taken once there too, so the same response posted again finds it used. Completion is the same.

The session records the provider it opened through; `GET /me/sessions` answers `method: oidc` or `saml` and the provider's slug.

`return_to` is honoured only when it is a path on the instance: one leading `/`, no backslash, no control or whitespace character, no scheme or host. Anything else, such as `//evil.example` or `https://evil.example`, is ignored and the sign-in lands on `/`.

An unknown or disabled provider answers `404` on all its addresses, and so does an address of the other kind: the ACS and the metadata of an OIDC provider, the callback of a SAML one. Disabling a provider keeps its members, identities and grants and stops its sign-ins.

A provider with identities, or whose group mappings made a grant, cannot be deleted (`409 provider_in_use`, `used_by` naming `identities` and `grants`); disable it instead.

## Linking and unlinking identities

A member signed in with a password links a provider to their account, so they can sign in through it next time, from their settings. A member's ways in are their password and their identities, the subjects providers know them by.

- `GET /me/identities` answers `{password: {set, set_at}, identities}`: each identity with its provider (`provider`, `provider_name`, `kind`, `provider_enabled`), the `subject` and when it was `linked_at` and last used (`last_sign_in_at`). An API token may read it.
- `POST /me/identities/<slug>/link` (a session only) starts linking. It takes an optional `return_to`, honoured under the rules of a sign-in's but falling back to `/settings`, answers `{start_url}`, the address to send the browser to, and sets the attempt cookie. The attempt is bound to the browser and to the session that started it: finishing it in another browser, in a session opened afterwards or after signing out links nothing. When the provider answers, the subject is linked to the member **whatever its email**, with no check against the provider's domains, and the browser lands on `return_to`. Linking a subject that is already linked to the same member changes nothing. A disabled or unknown provider is `404`, and one the instance cannot reach `502 provider_unreachable`. Recorded as `identity.linked`.
- `DELETE /me/identities/<identity>` (a session only) unlinks an identity of the caller. An instance admin unlinks a member's with `DELETE /members/<member>/identities/<identity>` and lists them with `GET /members/<member>/identities`, for example to undo a wrong link. Recorded as `identity.unlinked`.
- **The last way in stays.** Unlinking an identity, or removing the password with `DELETE /me/password` (a session only, recorded as `password.removed`; [Access](access.md#your-own-account) covers it), is refused `409 last_auth_method` when no other usable way in would remain. A usable way in is a password, or an identity at an enabled provider: an identity at a disabled provider is listed but signs nobody in, so unlinking it is always allowed. A deactivated member signs in nowhere, so an instance admin may unlink their last identity. The identity of a provisioned member (one created through the provider's SCIM token) at that provider is the directory's: unlinking it is refused `409 member_provisioned`.

Where a link fails after the instance has identified its attempt, the browser lands on `/settings?error=<code>` (a state or completion token it cannot identify, or a provider disabled meanwhile, ends as a sign-in's failure does): `identity_linked` when the subject is linked to another member, `attempt_mismatch` for a browser or session that did not start the link, `sso_failed` or `idp_error` as at sign-in.

## Sign-in errors

Every failure redirects to `/sign-in?error=<code>`:

| Code | Meaning | What to do |
|---|---|---|
| `sso_failed` | The sign-in could not be verified: the state was unknown, used or expired, the token's signature, issuer, audience, expiry or nonce was wrong, the code exchange was refused (check the client secret), a SAML response failed one of the [checks above](#what-the-instance-checks-of-a-response) (not signed by the provider's certificate, another audience or destination, not an answer to the request, expired, replayed, wrapped), the provider could not be reached, or the attempt was gone when it was completed. | Try again. If it repeats, `POST …/test` the provider and read the server's log: a record `single sign-on failed` carries the cause. |
| `idp_error` | The identity provider refused the sign-in itself, for example the person denied access. | Ask the identity provider's administrator. |
| `member_not_found` | No member matches the person: their email is outside the provider's domains or (OIDC) unverified, linking by email is off, provisioning is off, or they hold an invitation they have not accepted. | Add the domain, switch `link_by_email` or `auto_provision` on, or invite the person. |
| `member_deactivated` | The member was deactivated. The failure is audited as `auth.sign_in_failed`. | An instance admin may reactivate them. |
| `attempt_mismatch` | The browser completing the sign-in is not the one that started it. | Start again in the same browser. |
| `identity_linked` | Only when linking, on the settings page: the provider's subject is already linked to another member. | An instance admin unlinks it from that member, or the person links a different account at the provider. |

## Audit and metrics

A first sign-in records `member.provisioned` or `identity.linked` (or both), unlinking an identity `identity.unlinked`, the access the member's groups gave or took away as [group mappings](#group-mappings) record it, and every sign-in `auth.signed_in` with its `method` (`oidc` or `saml`) and `provider`; a refused sign-in of a known member (one the subject is linked to, or one holding an email the provider vouches for) records `auth.sign_in_failed` with the `reason`: the member's status when it refuses them (`deactivated`, `invited`), or `not_linked` for an active member when the provider does not link by email.

`flagnetic_sso_sign_ins_total{kind,result}` counts sign-ins that ended, by `kind` (`oidc` or `saml`, `unknown` when the sign-in failed before its provider was known) and `result`: `succeeded`, `linked` for a link that completed, or the code above.
