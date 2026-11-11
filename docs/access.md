# Access

Who may do what in a Flagnetic instance: permissions, the roles that list them, the grants that give a member a role in a project, and the API tokens scripts and pipelines use.

## Permissions

Every operation of the management API declares the one thing it needs in the OpenAPI document (`x-permission`): a permission of the catalog, judged in the project the operation addresses, or one of `public` (anyone), `member` (any credential acting for a member) and `instance_admin`. A permission is named `entity.verb`.

| Permission | Allows | Token scope | Members only |
|---|---|---|---|
| `project.read` | every ordinary read in a project, its audit log included, and the uniformity check | `read` | |
| `project.update` | renaming a project and changing its description | `project:admin` | |
| `environment.manage` | creating, renaming and deleting environments (a protected one only once unprotected) | `project:admin` | |
| `environment.protect` | protecting an environment and stopping protecting it | `project:admin` | ✓ |
| `declaration.manage` | declaring, renaming and deleting unit types and attributes; declaring and dismissing discovered attributes | `flags:write` | |
| `grant.manage` | making and removing grants | `project:admin` | ✓ |
| `project_token.manage` | listing, creating and revoking project tokens | `project:admin` | ✓ |
| `flag.edit` | creating flags and changing their definition | `flags:write` | |
| `flag.archive` | archiving, restoring and deleting flags | `flags:write` | |
| `flag_configuration.update` | replacing a flag's configuration in an environment, setting and removing single overrides, regenerating a rollout's salt, copying a configuration from another environment | `flags:write` | |
| `flag_configuration.kill` | switching a flag off in an environment with the kill switch | `flags:write` | |
| `flag_configuration.revert` | reverting a flag's configuration in an environment to an earlier version | `flags:write` | |
| `segment.edit` | creating segments and changing them | `flags:write` | |
| `segment.delete` | deleting segments | `flags:write` | |
| `sdk_key.manage` | creating, renaming, rotating and revoking an environment's SDK keys and changing their allowed origins | `project:admin` | |
| `experiment.edit` | creating, changing and cloning experiments, and regenerating a draft's salts | `experiments:write` | |
| `experiment.delete` | deleting draft experiments | `experiments:write` | |
| `change_request.propose` | proposing changes as change requests: creating them, editing, rebasing, opening, cancelling and applying one's own, copying any | `flags:write` | |
| `change_request.approve` | deciding change requests: approving and rejecting another's, and cancelling or applying another's | `flags:write` | ✓ |
| `webhook.manage` | creating, reading, editing, enabling and deleting a project's webhook subscriptions, rotating their secrets, testing them, reading and redelivering their deliveries, and sending the stale-flag digest now | `project:admin` | |
| `sink.manage` | creating, reading, editing and deleting a project's sinks, rotating a webhook sink's secret, resuming a stopped sink, and listing, redelivering and discarding its dead letters; reading needs it too, since a sink's configuration names internal hosts, buckets and URLs that may carry tokens | `project:admin` | |
| `project.delete` | deleting a project with everything in it, from a signed-in session only | `project:admin` | ✓ |
| `project.import` | merging a project document into a project, in every environment whether it is protected or not | `project:admin` | ✓ |

A permission marked members only changes who has access to a project or whose review a change needs, so no project token exercises it, whatever its role (see [Project tokens](#project-tokens)): a pipeline cannot unprotect an environment, write it and protect it again, nor approve the change it proposed. A project token whose role holds `change_request.approve` is refused deciding a change request with `403 token_cannot_approve`, which says why more plainly than the permission it lacks.

A refusal for a missing permission answers `403 forbidden` and names the permission in the problem's `permission` member, so a script can ask for exactly that. An unknown project answers `404 not_found` before any permission is judged.

### How a request is judged

Before an operation's handler runs, one check in front of every operation judges the request, in this order, and the first refusal is the answer:

1. **Credential.** An `Authorization` header is an API token presented as a bearer, judged instead of any session cookie; a malformed, unknown, expired or revoked one is refused `401` even on a public operation. Without one, the session cookie is the credential, and one whose session has ended is refused `401 session_expired` and cleared. A token anywhere else, such as the query string, is never read, since URLs end up in logs and browser histories. Only a public operation is open without a credential (`401 unauthenticated` otherwise), and a public operation ignores a session cookie, so an ended session never stands between a member and signing in again.
2. **Origin.** A request signed in by the session cookie that the browser says comes from an origin other than the console's is refused `403 cross_origin`, a read as much as a write and a subdomain of the same site as much as an unrelated one.
3. **Project token reach.** A project token reaches its own project and `GET /me` alone (`403 forbidden`).
4. **Session.** An operation only a signed-in member may perform refuses any API token `403 session_required`.
5. **Scope.** A token lacking the scope the operation's permission falls under is refused `403 insufficient_scope`.
6. **Project.** An operation addressing an unknown project answers `404 not_found`.
7. **Permission.** A caller without the permission in the project is refused `403 forbidden`, naming it.
8. **Body.** An operation that opens a session (signing in, accepting an invitation) refuses a body not sent as `application/json` with `415 unsupported_media_type`, since that is what a form on another site would send.

The refusals of steps 1 to 7 are counted per operation and code in the metric `flagnetic_management_refusals_total`, so a sudden run of one code (a revoked token still in use, say) shows on a dashboard.

## Roles

A role is a fixed list of permissions. The four built-in roles are the same on every instance:

| Permission | viewer | developer | maintainer | owner |
|---|---|---|---|---|
| `project.read` | ✓ | ✓ | ✓ | ✓ |
| `project.update` | | | | ✓ |
| `environment.manage` | | | ✓ | ✓ |
| `environment.protect` | | | | ✓ |
| `declaration.manage` | | | ✓ | ✓ |
| `grant.manage` | | | | ✓ |
| `project_token.manage` | | | | ✓ |
| `flag.edit` | | ✓ | ✓ | ✓ |
| `flag.archive` | | | ✓ | ✓ |
| `flag_configuration.update` | | ✓ | ✓ | ✓ |
| `flag_configuration.kill` | | ✓ | ✓ | ✓ |
| `flag_configuration.revert` | | ✓ | ✓ | ✓ |
| `segment.edit` | | ✓ | ✓ | ✓ |
| `segment.delete` | | | ✓ | ✓ |
| `sdk_key.manage` | | | ✓ | ✓ |
| `experiment.edit` | | ✓ | ✓ | ✓ |
| `experiment.delete` | | | ✓ | ✓ |
| `change_request.propose` | | ✓ | ✓ | ✓ |
| `change_request.approve` | | | ✓ | ✓ |
| `webhook.manage` | | | ✓ | ✓ |
| `sink.manage` | | | ✓ | ✓ |
| `project.delete` | | | | ✓ |
| `project.import` | | | | ✓ |

`GET /roles` lists every role with its permissions, so a client can explain which roles hold a permission a caller lacks. Later releases add permissions as features arrive and write them into the built-in roles.

## Grants

A grant is one role held by one member in one project. A project owner grants a member one or more roles (`POST /projects/{project}/grants`) and removes one grant at a time, leaving the others (`DELETE /projects/{project}/grants/{grant}`). A member's permissions in a project are the union of their grants' roles there, so holding more grants never takes anything away. `GET /projects/{project}/members` lists the members holding grants with their grants side by side, and `GET /projects/{project}/me` answers the caller's grants and effective permissions there.

- A grant has a `source`: `manual` for one made by hand, and `sso` for one an auth provider's [group mapping](sso.md#group-mappings) made, which then names the `provider` (its slug). Each source owns only its own grants, so they sit side by side: a mapping never skips a project where the member holds a role by hand, and a grant by hand may be added beside a mapped one. A mapped grant cannot be removed by hand (`409 sso_sourced`); it goes when the member leaves the group (in a sign-in's claim or in the provider's SCIM group), or the mapping is removed.
- The same role twice for the same member is refused `409 already_granted`; a deactivated member `409 member_deactivated`. A member still invited is refused `409 member_invited`, for a role and for the instance admin switch alike, until they accept their invitation: until an invitation is [mailed](#inviting-with-mail), its link is held by whoever issued it, who could accept it as the person invited, so what an invited member is given would be given to whoever holds their link. An invitation carries one role in one project; give the rest once the person has joined.
- Nobody makes or removes a grant of a role holding a permission they lack in the project (`422 role_not_allowed`, naming it in `permission`), so managing access never escalates into more access. With the built-in roles only owners manage grants, and a signed-in owner holds every permission of a project; an owner's token holds only what its scopes leave it, so one without `flags:write` cannot hand out maintainer or owner.
- An instance admin holds every permission in every project without a grant, and is every project's backstop: a project's creator receives no grant, and removing the last owner's grant is allowed.
- A member sees in `GET /projects` the projects where they hold a grant; an instance admin sees every project.

The console's project members screen lists every member holding a grant with each grant's role, source (a badge: `Manual`, or `SSO` with the provider), maker and time, and lets an owner grant a role and remove one grant at a time; the remove action of a grant a group mapping made is disabled, saying that only the provider's group mappings remove it. Reading every member of the instance is an instance admin's alone, so an instance admin grants a role to anyone, while an owner who is not one grants further roles to the project's own members.

### Inviting to a project

An owner brings someone who has no account into their project in one step, instead of asking an instance admin to invite them and then granting a role: `POST /projects/{project}/invitations` takes the person's `email` and `display_name` and one built-in `role`, and needs `grant.manage` in the project. It creates an invited member, as `POST /members` does, and answers the same one-time link to pass on; accepting the link makes the member active and grants the role in the project, by hand, in the name of the member who invited.

- The role is capped like any grant: one holding a permission the inviter lacks in the project is refused `422 role_not_allowed`, naming it in `permission`, and a role that is none of the built-in ones `422 invalid_grant`. A project token never holds `grant.manage`, so it cannot invite.
- An email the instance holds, whatever its case, is refused `409 already_exists`: grant that member a role instead. The one exception is a member still invited whose latest invitation is to this project, who is invited again by the member who issued that invitation or by an instance admin: the new link replaces the old one and carries the new role. Anyone else is refused `409 already_exists` as well, since issuing an invitation again hands its link to whoever asks while the instance cannot mail it.
- An instance admin's invitation (`POST /members`) may carry a role in a project too, as `grant` with the project's key and the role; an unknown project or role is refused `422 invalid_grant`.
- A person whose email an [auth provider](sso.md#accepting-an-invitation-through-a-provider) vouches for may accept by signing in through it instead of setting a password: the member is activated and granted the role alike. `GET /auth/invitations/{secret}` lists those providers in `providers` and says in `password_allowed` whether a password may be set. The invitation page shows the password form only when it may, a *Sign in with* button for each provider, and says so when neither is available.
- Until the invitation is accepted it grants nothing. `GET /auth/invitations/{secret}`, which anyone holding the link may read, shows the project (its key and name), the role and the name of the member who invited, so the person knows what they accept.
- The grant is dropped, and the invitation still activates the person, when its inviter can no longer hand it out before it is accepted: when they lose `grant.manage` in the project (a grant of theirs removed that leaves them without it, or an instance admin demoted who holds no grant with it in the project) or when they are deactivated. The audit log of the project records `invitation.grant_revoked`, naming the person, the role and the `reason`, `inviter_lost_permission` or `inviter_deactivated`. An instance admin never loses it for lack of a grant.
- Reissuing an invitation (`POST /members/{member}/invitation`) keeps its role and the member who invited, who is the one the grant is made in the name of, only while that member still holds `grant.manage` in the project; a reissue never widens access.
- The project's audit log records the invitation (`member.invited`, or `member.invitation_reissued` for a second one) and, when the link is accepted, `grant.created` with the inviter as actor; the acceptance itself, `member.invitation_accepted`, is the member's own and belongs to the instance.
- `GET /projects/{project}/invitations`, with `grant.manage` in the project, lists the members still invited whose latest invitation grants a role there, expired or not, by email: each with the `role` accepting grants, the member who invited (`invited_by`) and when the link stops working (`expires_at`). An invitation whose grant was dropped, or that grants a role in another project, is not listed.

The console's project members screen invites someone by email with one of the roles the caller may grant there, and lists the invited members below the members holding grants; inviting one of them again replaces their link with the same role. The instance members screen invites with an optional project and role. Either says the invitation was mailed, or shows its link once to pass on.

### Inviting with mail

Once the instance has an [SMTP server](mail.md) and `FLAGNETIC_BASE_URL` is set, every invitation is mailed to the person it invites instead of shown: an instance admin's (`POST /members`), one to a project (`POST /projects/{project}/invitations`), a reissue (`POST /members/{member}/invitation`, or inviting a person to the same project again) and the one a reactivation issues. The answer's `invitation` then says `mailed: true` and carries `expires_at` but no `url`, so the link reaches only the person invited and accepting it proves they hold the address. Otherwise the answer says `mailed: false` and carries the link in `url`, as described above.

- The mail names who invited, when someone did, and for an invitation to a project the project and the role accepting grants there; it carries the link, `FLAGNETIC_BASE_URL` followed by `/invitations/<secret>`, and when it expires.
- It is written in the invited person's language when the instance knows it (a member reactivated who chose one), else in the language of the member who invited, else in `FLAGNETIC_DEFAULT_LOCALE` (see [Languages](mail.md#languages)).
- The mail leaves after the invitation is stored. A send that fails never undoes the invitation: it is logged and counted (see [Mail](mail.md#what-happens-when-a-send-fails)), and reissuing the invitation mails a fresh link and kills the one that was lost.

## Resetting a member's password

A member who cannot sign in gets a new password through a password reset: a one-time link that lets them set one. An instance admin issues it with `POST /members/{member}/password-reset`, which answers `201` with the link in `url` and when it stops working in `expires_at`. The link is shown that once and never mailed, so the instance admin passes it on: it is `FLAGNETIC_BASE_URL` (or, when unset, the origin the request came to) followed by `/password-reset/<secret>`. It works once and for an hour.

- Only an active member is given one. A member still invited is refused `409 member_not_active`: issue their invitation again instead (`POST /members/{member}/invitation`). A deactivated member is refused `409 member_deactivated`, and an unknown one `404`.
- Issuing a second link leaves the first open. They are alternatives: completing any of them ends all the others.
- `GET /auth/password-reset/{secret}` needs no credential and shows whose password the link sets: the email masked, as an invitation shows it (`g***@example.com`), the display name and when the link expires. A link that was never issued, has been used or ended, has expired, or whose member has been deactivated since is `410 reset_gone`, alike, so a link tells nobody whether it ever existed.
- `POST /auth/password-reset/{secret}` takes the new `password`, as `application/json` of at most 8 KiB, and needs no credential either. The [password policy](operations.md#signing-in-to-the-console) applies (`422 weak_password`; the link stays usable). On success the password is set, the lockout on the member's email is cleared, every session of the member is revoked, every other open password reset and every open invitation of theirs ends, and a new session is opened with its cookie, as signing in opens one. The answer is the caller, as `GET /me` gives it.
- Completing counts against the address throttle like a failed sign-in until the secret proves to open a password reset, so an address hammering it is throttled together with its sign-ins (`429 rate_limited`).
- A password change (`POST /me/password`) and a deactivation end the member's open password resets too, so an old link never works after either, and reactivating the member does not bring it back.
- The secret is stored only as its SHA-256, and is never written to a log line (requests carrying it are logged with the route's pattern) nor to the audit log. The instance audit log records `password.reset_issued` in the name of the instance admin, and, when the link is completed, `password.reset` (with the number of sessions it revoked) and `auth.signed_in` in the name of the member. Used and expired password resets are deleted 30 days after they ended.

In the console, an instance admin issues one with **Issue a password reset link** on an active member's page, after a confirmation, and sees the link once; the action is not offered for an invited or a deactivated member, and the refusal is explained if the member changed in the meantime. The link opens a page that names whose password it sets and takes the new one twice; the password policy's refusals are said under it, and a link that opens nothing any more says so, with the way to ask for a new one. Setting the password signs the member in and opens the console.

### Asking for a password reset by mail

A member who forgot their password asks for a reset themselves with `POST /auth/password-reset` and their `email` (a public operation, `application/json` of at most 8 KiB, like the other `/auth` bodies; anything else is refused `415 unsupported_media_type`). The answer is `202 Accepted` at once, empty, whoever the email belongs to and whether or not anything comes of it, so it tells nobody which addresses hold an account. The lookup, the issuing and the mail happen after the answer, so the request does not take longer for an address that has an account. Every request counts against the address throttle that sign-in, invitation acceptance and password reset completion share, and is never taken back: past a hundred from one address within fifteen minutes it is refused `429 rate_limited` with `Retry-After`, whether or not the email exists. The replica also runs at most 256 background jobs at once (mails and these requests); one started past that is dropped, logged and counted in `flagnetic_background_dropped_total{kind}`.

A password reset is issued and mailed only when all of these hold; otherwise nothing happens, silently:

- The instance has an [SMTP server](mail.md) and `self_service_password_reset` is on in the [auth settings](operations.md#signing-in-to-the-console) (it is on on a fresh instance). `GET /auth/methods` answers `password_reset_available` for exactly this, which is what the sign-in page checks before it offers the forgotten-password link. Local login must be on too, and the email outside every [SSO-enforced domain](sso.md#sso-enforcement-and-local-login): otherwise the request is answered `202` all the same and nothing is mailed.
- The email, whatever its case, belongs to an active member who holds a password. An unknown address, an invited member (who has an invitation), a deactivated member and a member who signs in through an auth provider only get nothing.
- No reset the member asked for was issued in the last 5 minutes. The interval is counted from the issuing, so a request that issued a reset whose mail the relay then refused still counts, and a request repeated while the relay is down does not hammer it. A reset an instance admin issued does not count, and does not delay one the member asks for.

The mail is written in the member's language and carries the same link an instance admin's reset does: `FLAGNETIC_BASE_URL` followed by `/password-reset/<secret>`, valid for an hour and usable once. It is completed with `POST /auth/password-reset/{secret}` as described above, with the same effects. A reset issued this way is recorded as `password.reset_requested` in the name of the member, without its secret; the mail's failure is logged and counted like any other (see [Mail](mail.md#what-happens-when-a-send-fails)).

An instance admin switches the self-service reset off with `self_service_password_reset: false` in the auth settings, or with **Self-service password reset** under **Auth settings** in the console. That removes only the member's own way: an instance admin can still issue a password reset.

In the console, the sign-in page offers **Forgot your password?** only while `password_reset_available` is true. It opens a page (`/forgot-password`) that takes the email and answers with the same sentence whatever it is; while the instance mails no reset, that page says so and points to an instance admin instead of offering the form.

## API tokens and scopes

A script authenticates with an API token in the `Authorization` header as a bearer. A token carries scopes, which only narrow what it may do: each permission falls under one scope, and a token exercises a permission only when it carries that scope. The scopes are `read`, `flags:write`, `experiments:write`, `project:admin` and `instance:admin`; a token carries `read` alone unless asked for more, and `read` beside every write scope. A scope the token lacks is refused `403 insufficient_scope` before an unknown project is reported, so the refusal is the same whether the project exists or not. The operations of the instance (`instance_admin`) need an instance admin, whose token carries `instance:admin` to change anything there and `read` to read. A session is narrowed by no scope.

A token's expiry lies in the future and at most the instance's maximum token lifetime ahead (365 days on a fresh instance); an instance admin can lower that lifetime or require every token to expire in the auth settings. Every caller reads both from `GET /me` (`max_token_lifetime_seconds`, `require_token_expiry`), and the console offers only the expiries they allow. Without an expiry a token never expires. An expired token is refused `401 token_expired`, a revoked one `401 token_revoked`, an unknown one `401 invalid_token`, each named in `WWW-Authenticate`. A token's value is shown once, when it is created; afterwards only its hint (its prefix and first four characters) and when and from where it was last used, recorded at most once a minute.

### Personal access tokens

A personal access token (`fn_pat_…`) acts for the member who created it, with that member's permissions narrowed by its scopes. A member creates, lists and revokes their own under `/me/tokens`; `instance:admin` is for an instance admin alone (`422 scope_not_allowed`). An instance admin lists and revokes any member's under `/members/{member}/tokens`. Deactivating a member revokes every personal access token they hold.

### Project tokens

A project token (`fn_prj_…`) belongs to a project rather than to a person, for a pipeline. An owner creates it holding one built-in role in the project, fixed for its life (`/projects/{project}/tokens`). Its permissions are its role's narrowed by its scopes, minus `grant.manage`, `project_token.manage`, `environment.protect`, `change_request.approve`, `project.delete` and `project.import`: whatever its role, a project token never changes who has access to the project, which environments are protected, nor decides a change request, never deletes the project, and never imports a document, which writes protected environments without a review. It reaches its own project and `GET /me` alone (`403 forbidden` anywhere else, the project list included), never carries `instance:admin`, and keeps working when the member who created it leaves. The console's project tokens screen, shown to an owner, lists them with their role, scopes, expiry, creator and last use, creates one showing its value once, and revokes one.

### Only a session creates a credential

Creating a personal access token or a project token, deleting a project, and every write to a member's own account (their name, password, sessions and tokens), needs a signed-in session: any API token is refused `403 session_required`, however wide its scopes. A leaked token therefore can neither mint another one that outlives its expiry or its revocation, nor sign its owner out or hide its siblings, nor erase a project. The cost is that a script cannot rotate its own token: a person does it, signed in, which is when someone should look at what the script holds. `flagnetic admin bootstrap` is the one other way to a token, since it runs with the database's own credentials.

## Auth providers

An instance admin lets members sign in through an identity provider by adding an auth provider under `/instance/auth-providers` (`instance_admin`). There are two kinds, OpenID Connect (`oidc`) and SAML (`saml`); [Single sign-on](sso.md) covers setting one up, what a sign-in does and its errors.

- `POST /instance/auth-providers` adds one: a `slug` (a lowercase letter, then lowercase letters, digits, hyphens and underscores, up to 64; it names the provider in the sign-in button and in its URLs, and never changes), the `kind` (`oidc` or `saml`), a `display_name`, the `domains` and the kind's own fields. An OIDC provider takes the `issuer`, the `client_id` and the `client_secret`, and optionally `scopes` (default `openid email profile`) and `require_verified_email` (default true). A SAML provider takes its identity provider's metadata as `idp_metadata_url` or `idp_metadata_xml`, and optionally `sign_requests` (default true) and `name_id_format` (default `unspecified`). Both take `claims` (the names of the email, name and groups claims or attributes, default `email`, `name` and `groups`), `enabled` (default true), `link_by_email` (default false) and `auto_provision` (default false). A field of the other kind is refused (`422 invalid_auth_provider`, violation `not_for_kind`). The slug names the provider in every other path: `GET`, `PATCH` and `DELETE /instance/auth-providers/{provider}`.
- **Domains.** `domains`, at least one and lowercased, are the email domains the provider vouches for. Only an email in one of them may be linked to an existing member by email or provisioned on first sign-in; a person whose identity is already linked signs in whatever their email says.
- **The client secret** is sealed under the instance's secret key and never read back: the provider answers `client_secret_set`. An edit that sends no `client_secret` keeps the stored one, unless it changes the issuer or the client ID: that edit is refused `422 invalid_auth_provider` with a violation at `/client_secret`, so the stored secret is never presented to an issuer it was not given for.
- **The issuer** must be `https`, except on a loopback address (`localhost`, `127.0.0.0/8`, `::1`): an `http` issuer elsewhere is refused `422 insecure_url`, since its discovery document, which says where tokens are signed and sent, could be altered on the way. Private networks are allowed. Issuer, client ID and every other field are checked as they are written, each violation naming its field under `422 invalid_auth_provider`.
- **The redirect URI** to register at an OIDC provider is answered as `redirect_uri`, built from `FLAGNETIC_BASE_URL`: `<base URL>/sso/<slug>/callback`. A SAML provider answers `acs_url`, `entity_id` and `metadata_url` instead, the addresses to register at its identity provider, with the identity provider's `idp_entity_id`, `idp_sso_url`, `idp_certificates` and `idp_metadata_refreshed_at` read from its metadata. Its generated key pair is sealed and never answered; the certificates of the pair are answered as `sp_certificates` (`sha256`, `not_after`, and `status` `current` or `next`) with `rotation_pending`, and [`POST …/rotate-certificate`](sso.md#rotating-the-certificate) and `…/rotate-certificate/complete` replace the pair in two steps. An identity provider's metadata fetched from a URL is [read again daily](sso.md#refreshing-the-metadata). Without the base URL a provider cannot be created, edited or enabled (`422 base_url_required`).
- **Edits** (`PATCH`) need `If-Match` with the provider's version, as every versioned entity does, and change any field but `slug` and `kind`; an absent field is left as it is, and `claims` names only the claims to rename. Whether the provider is enabled is changed with `POST …/enable` and `POST …/disable`: a disabled provider is not offered at sign-in, answers `404` at its `/sso` addresses, and keeps its members, identities and grants.
- **Testing.** `POST …/{provider}/test` fetches the OIDC provider's discovery document now, enabled or not, and answers what it found: the issuer, the authorization, token, JWKS and user info endpoints, and the scopes and PKCE methods it lists. For a SAML provider it fetches the metadata again, or reads the pasted one, and answers the identity provider's entity ID, single sign-on address and certificates. A provider that cannot be reached, answers anything but what it should, or answers more than a megabyte is `502 provider_unreachable`, with the `failure`. Nothing is stored.
- **Group mappings.** `GET` and `PUT …/{provider}/group-mappings` read and replace what the provider's directory groups grant, a role in a project or instance admin, and replacing runs them at once for every member who signed in through the provider or is in one of its SCIM groups, answering `{granted, revoked, kept_instance_admin}`. [Single sign-on](sso.md#group-mappings) covers them in full.
- **Deleting** a provider that something refers to, such as a member's linked identity, a SCIM token not revoked, a member it provisioned or a grant its group mappings made, is refused `409 provider_in_use`, with what refers to it in `used_by`: disable it instead. A provider an [SSO-enforced domain](sso.md#sso-enforcement-and-local-login) names can be neither deleted, disabled nor edited to drop that domain (`used_by` naming `enforced domains`), and while local login is off the only enabled provider can be neither disabled nor deleted (`only way in`).
- `GET /auth/methods` (public) lists the enabled providers as `{slug, display_name, kind}`, which is what the sign-in page offers, says in `local_login_enabled` whether a password signs anybody in and in `password_reset_available` whether a member can [ask for a password reset by mail](#asking-for-a-password-reset-by-mail), and, given `?email=`, names in `enforced_provider` the provider an [enforced](sso.md#sso-enforcement-and-local-login) email must sign in through.

Creating, editing, enabling, disabling and deleting are audited as `auth_provider.created`, `.updated`, `.enabled`, `.disabled` and `.deleted`, a daily metadata refresh as `.metadata_refreshed` (or `.metadata_refresh_refused`), a certificate rotation as `.certificate_rotation_started` and `.certificate_rotation_completed`, and replacing group mappings as `group_mappings.updated`, with the client secret written `[redacted]` and, for a SAML provider, the key left out and the identity provider's certificates as their fingerprints. A member's sessions say how they signed in: `method` is `local`, `oidc` or `saml`, and `provider` names the auth provider's slug for the latter two.

## Provisioned members

A member an identity provider created or adopted through [SCIM](scim.md) is a *provisioned member*: the provider's directory decides their email, name and whether they are active. `GET /members` answers the `provider` (its slug) of each, `null` for any other. The management API refuses to change by hand what the directory decides, `409 member_provisioned`: editing the email or the display name (`PATCH /members/{member}`, or `PATCH /me` for the member's own name), deactivating (`POST /members/{member}/deactivate`) and reactivating. A request that asks for what is already so is no change. Grants, the instance admin switch, the member's language and their tokens stay the instance's to manage. The console marks a provisioned member with a badge naming the provider, and shows **Edit**, **Deactivate** and **Reactivate** disabled on their page with that reason.

## Your own account

A signed-in member renames themself and chooses the console's language with `PATCH /me` (`display_name`, and `locale`: `en`, `ru`, or `null` to follow the browser), recorded as `member.updated`. Their email and the instance admin switch are an instance admin's to change.

`POST /me/password` changes the password: it takes the current one and the new one, which the password policy must accept (`422 weak_password` at `/new_password`). Every other session of the member is revoked, so a device that learned the old password is signed out, and their open invitations and [password resets](#resetting-a-members-password) end; the one making the change stays signed in. The change is recorded as `password.changed` with the number of sessions it revoked. The new password is checked first, so a weak one is refused without the current one being checked or counted. A wrong current password is refused `422 invalid_credentials`, with a violation at `/current_password`, and counts as a failed sign-in of the member's email and address, as at the sign-in page: the fifth wrong attempt locks the email, and the next attempt, here or at sign-in, is refused `429 locked` until the lock ends. From an address the member signed in from within the last 30 days, a [known source](operations.md#signing-in-to-the-console), the failures are counted for that address alone and lock the email there only, as a sign-in from it would. A session left open on a shared device therefore cannot be used to guess the password.

`GET /me/identities` shows how the member signs in: whether they hold a password and when they set it, and their identities at [auth providers](sso.md#linking-and-unlinking-identities), which they link with `POST /me/identities/{provider}/link` and unlink with `DELETE /me/identities/{identity}`. `DELETE /me/password` removes the password, so the member signs in only through their identities; it is refused `409 last_auth_method` when no identity at an enabled auth provider that [SSO enforcement](sso.md#sso-enforcement-and-local-login) allows remains, even while local login is off (the same refusal unlinking the last way in meets), answers `204` and records nothing for a member who holds no password, and ends the member's open invitations and password resets, as a password change does, so an old link cannot set one again. It is recorded as `password.removed`. Linking, unlinking and removing a password need a signed-in session: an API token is refused `403 session_required`. An instance admin reads and unlinks a member's identities under `/members/{member}/identities`.

The console's personal settings do all of this: the **Sign-in methods** page shows the password and its date, changes or removes it, lists the identities with their provider, account, last sign-in and a *Provider disabled* mark, links an enabled provider that is not linked yet (it sends the browser to the provider and brings it back to the page, or says why nothing was linked) and unlinks an identity, each refusal explained in place. A member with no password is told how to set one: through the forgotten-password page, or an instance admin's reset link. Other pages of the personal settings list the devices the member is signed in on and sign them out, and create, show once and revoke their personal access tokens. An instance admin sees a member's identities in the *Sign-in methods* tab of the member's page and unlinks a wrong one there.

## In the console

The console reads what the member may do in a project from `GET /projects/{project}/me` and which roles hold each permission from `GET /roles`, and never infers anything from a role's name. The project navigation shows only the screens the member can obtain something from. An action they lack the permission for is either left out or shown disabled with the reason, such as "Needs the permission to manage environments, held by Owner and Maintainer.": a tooltip by pointer and by keyboard focus, and the action's description for a screen reader, so the member knows whom to ask. This only spares a member a refusal; the server judges every request the same way whatever the console showed.

## Instance admin operations

Besides the members, sessions and tokens of every member, an instance admin holds the instance's settings:

- `GET` and `PUT /instance/auth-settings`: session timeouts, the longest an API token may live and the password policy (see [Signing in to the console](operations.md#signing-in-to-the-console)). Recorded as `auth_settings.updated`.
- `GET`, `PUT` and `DELETE /instance/smtp`, and `POST /instance/smtp/test`: the SMTP server the instance mails through and a test mail to the caller (see [Mail](mail.md)). Recorded as `smtp.updated` and `smtp.deleted`, never with the password.
- `GET` and `POST /instance/scim-tokens`, and `DELETE /instance/scim-tokens/{token}`: the credentials identity providers [provision members](scim.md) with. Creating one needs a session. Recorded as `scim_token.created` and `scim_token.revoked`, never with the value.
- `GET /audit` and its export, the instance's [audit log](#the-audit-log).

## The audit log

Every change is recorded as an audit entry inside the transaction that makes it, so a change that is rolled back leaves no entry and a committed one always has one. An entry holds when it happened, the project it belongs to (none for a change to the instance, such as a sign-in or an auth settings change), the environment it addressed if any, the actor, the action (`entity.verb`, such as `environment.updated`), the entity with its kind, identifier and key as they were, the values before and after, and the ID of the request that made the change. The actor is a member, an API token (with the member a personal access token acts for), an auth provider's group mapping (kind `sso`, named by the provider's slug), a SCIM token (kind `scim`, named by its hint), the CLI or the system, named as they were called when the change was made. Secrets never reach an entry: they are written `[redacted]`, and a token appears by its hint. Entries keep no link to their project that would delete them with it, so they outlive what they record. Nothing writes to the log through the API.

- `GET /projects/{project}/audit` lists a project's entries for anyone holding `project.read` there, and `GET /projects/{project}/audit/{entry}` reads one.
- `GET /audit` lists every entry of the instance for an instance admin, with a `project` filter taking a project's identifier.
- Filters: `actor` (a member's identifier: their own changes and those of their personal access tokens), `actor_kind`, `action`, `entity_kind`, `entity_id` (which follows an entity whatever key it had), `environment`, `kill_switch` (`true` for the uses of the [kill switch](flags.md#switching-on-and-off-the-kill-switch) alone, `false` for every other entry), `change_request` (a change request's identifier: the entries its [application](change-requests.md#approving-and-applying) wrote, which carry it as `change_request_id`), `from` and `to` (both included), and `q`, text found in the action, the entity's key or the actor's name, whatever its case.
- Newest first, 50 entries a page unless `limit` asks for up to 200; the answer's `next_cursor` fetches the next page.
- `GET /projects/{project}/stream`, for anyone holding `project.read` there, is a server-sent event stream with a change notice for each entry the project records, from whichever replica made the change: the entity's kind and key, the environment's key, the entity's version and the actor, never the values. A client reconnecting with `Last-Event-ID` hears the entries it missed, up to 1,000, or is told to refetch what it shows. The caller's access is resolved again at every notice and 15-second heartbeat, and the stream ends once it is gone. A member, or a project token, holds at most 10 streams open on one replica (`429 too_many_streams`).

The console shows both logs, the project's under **Audit log** in the project navigation and the instance's under **Audit log** in the instance navigation, with these filters and each entry's values before and after. Both pages download every entry the filters on screen match as JSON Lines or CSV (see [Downloading the audit log](operations.md#downloading-the-audit-log)); a filter matching more than the cap is explained, and narrowing it with **From** and **To** brings it under.
