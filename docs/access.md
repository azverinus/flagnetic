# Access

Who may do what in a Flagnetic instance: permissions, the roles that list them, the grants that give a member a role in a project, and the API tokens scripts and pipelines use.

## Permissions

Every operation of the management API declares the one thing it needs in the OpenAPI document (`x-permission`): a permission of the catalog, judged in the project the operation addresses, or one of `public` (anyone), `member` (any credential acting for a member) and `instance_admin`. A permission is named `entity.verb`.

| Permission | Allows | Token scope | Members only |
|---|---|---|---|
| `project.read` | every ordinary read in a project, its audit log included, and the uniformity check | `read` | |
| `project.update` | renaming a project and changing its description | `project:admin` | |
| `environment.manage` | creating, renaming and deleting environments | `project:admin` | |
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
| `webhook.manage` | creating, reading, editing and deleting a project's webhook subscriptions | `project:admin` | |
| `project.delete` | deleting a project with everything in it, from a signed-in session only | `project:admin` | ✓ |

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
| `project.delete` | | | | ✓ |

`GET /roles` lists every role with its permissions, so a client can explain which roles hold a permission a caller lacks. Later releases add permissions as features arrive and write them into the built-in roles.

## Grants

A grant is one role held by one member in one project. A project owner grants a member one or more roles (`POST /projects/{project}/grants`) and removes one grant at a time, leaving the others (`DELETE /projects/{project}/grants/{grant}`). A member's permissions in a project are the union of their grants' roles there, so holding more grants never takes anything away. `GET /projects/{project}/members` lists the members holding grants with their grants side by side, and `GET /projects/{project}/me` answers the caller's grants and effective permissions there.

- The same role twice for the same member is refused `409 already_granted`; a deactivated member `409 member_deactivated`. An invited member may be granted roles before they join.
- Nobody makes or removes a grant of a role holding a permission they lack in the project (`422 role_not_allowed`, naming it in `permission`), so managing access never escalates into more access. With the built-in roles only owners manage grants, and a signed-in owner holds every permission of a project; an owner's token holds only what its scopes leave it, so one without `flags:write` cannot hand out maintainer or owner.
- An instance admin holds every permission in every project without a grant, and is every project's backstop: a project's creator receives no grant, and removing the last owner's grant is allowed.
- A member sees in `GET /projects` the projects where they hold a grant; an instance admin sees every project.

The console's project members screen lists every member holding a grant with each grant's role, source, maker and time, and lets an owner grant a role and remove one grant at a time. Reading every member of the instance is an instance admin's alone, so an instance admin grants a role to anyone, while an owner who is not one grants further roles to the project's own members.

## API tokens and scopes

A script authenticates with an API token in the `Authorization` header as a bearer. A token carries scopes, which only narrow what it may do: each permission falls under one scope, and a token exercises a permission only when it carries that scope. The scopes are `read`, `flags:write`, `experiments:write`, `project:admin` and `instance:admin`; a token carries `read` alone unless asked for more, and `read` beside every write scope. A scope the token lacks is refused `403 insufficient_scope` before an unknown project is reported, so the refusal is the same whether the project exists or not. The operations of the instance (`instance_admin`) need an instance admin, whose token carries `instance:admin` to change anything there and `read` to read. A session is narrowed by no scope.

A token's expiry lies in the future and at most the instance's maximum token lifetime ahead (365 days on a fresh instance); an instance admin can lower that lifetime or require every token to expire in the auth settings. Every caller reads both from `GET /me` (`max_token_lifetime_seconds`, `require_token_expiry`), and the console offers only the expiries they allow. Without an expiry a token never expires. An expired token is refused `401 token_expired`, a revoked one `401 token_revoked`, an unknown one `401 invalid_token`, each named in `WWW-Authenticate`. A token's value is shown once, when it is created; afterwards only its hint (its prefix and first four characters) and when and from where it was last used, recorded at most once a minute.

### Personal access tokens

A personal access token (`fn_pat_…`) acts for the member who created it, with that member's permissions narrowed by its scopes. A member creates, lists and revokes their own under `/me/tokens`; `instance:admin` is for an instance admin alone (`422 scope_not_allowed`). An instance admin lists and revokes any member's under `/members/{member}/tokens`. Deactivating a member revokes every personal access token they hold.

### Project tokens

A project token (`fn_prj_…`) belongs to a project rather than to a person, for a pipeline. An owner creates it holding one built-in role in the project, fixed for its life (`/projects/{project}/tokens`). Its permissions are its role's narrowed by its scopes, minus `grant.manage`, `project_token.manage`, `environment.protect`, `change_request.approve` and `project.delete`: whatever its role, a project token never changes who has access to the project, which environments are protected, nor decides a change request, and never deletes the project. It reaches its own project and `GET /me` alone (`403 forbidden` anywhere else, the project list included), never carries `instance:admin`, and keeps working when the member who created it leaves. The console's project tokens screen, shown to an owner, lists them with their role, scopes, expiry, creator and last use, creates one showing its value once, and revokes one.

### Only a session creates a credential

Creating a personal access token or a project token, deleting a project, and every write to a member's own account (their name, password, sessions and tokens), needs a signed-in session: any API token is refused `403 session_required`, however wide its scopes. A leaked token therefore can neither mint another one that outlives its expiry or its revocation, nor sign its owner out or hide its siblings, nor erase a project. The cost is that a script cannot rotate its own token: a person does it, signed in, which is when someone should look at what the script holds. `flagnetic admin bootstrap` is the one other way to a token, since it runs with the database's own credentials.

## Your own account

A signed-in member renames themself and chooses the console's language with `PATCH /me` (`display_name`, and `locale`: `en`, `ru`, or `null` to follow the browser), recorded as `member.updated`. Their email and the instance admin switch are an instance admin's to change.

`POST /me/password` changes the password: it takes the current one and the new one, which the password policy must accept (`422 weak_password` at `/new_password`). Every other session of the member is revoked, so a device that learned the old password is signed out; the one making the change stays signed in. The change is recorded as `password.changed` with the number of sessions it revoked. The new password is checked first, so a weak one is refused without the current one being checked or counted. A wrong current password is refused `422 invalid_credentials`, with a violation at `/current_password`, and counts as a failed sign-in of the member's email and address, as at the sign-in page: the fifth wrong attempt locks the email, and the next attempt, here or at sign-in, is refused `429 locked` until the lock ends. A session left open on a shared device therefore cannot be used to guess the password.

The console's personal settings do all of this, list the devices the member is signed in on and sign them out, and create, show once and revoke their personal access tokens.

## In the console

The console reads what the member may do in a project from `GET /projects/{project}/me` and which roles hold each permission from `GET /roles`, and never infers anything from a role's name. The project navigation shows only the screens the member can obtain something from. An action they lack the permission for is either left out or shown disabled with the reason, such as "Needs the permission to manage environments, held by Owner and Maintainer.": a tooltip by pointer and by keyboard focus, and the action's description for a screen reader, so the member knows whom to ask. This only spares a member a refusal; the server judges every request the same way whatever the console showed.

## The audit log

Every change is recorded as an audit entry inside the transaction that makes it, so a change that is rolled back leaves no entry and a committed one always has one. An entry holds when it happened, the project it belongs to (none for a change to the instance, such as a sign-in or an auth settings change), the environment it addressed if any, the actor, the action (`entity.verb`, such as `environment.updated`), the entity with its kind, identifier and key as they were, the values before and after, and the ID of the request that made the change. The actor is a member, an API token (with the member a personal access token acts for), the CLI or the system, named as they were called when the change was made. Secrets never reach an entry: they are written `[redacted]`, and a token appears by its hint. Entries keep no link to their project that would delete them with it, so they outlive what they record. Nothing writes to the log through the API.

- `GET /projects/{project}/audit` lists a project's entries for anyone holding `project.read` there, and `GET /projects/{project}/audit/{entry}` reads one.
- `GET /audit` lists every entry of the instance for an instance admin, with a `project` filter taking a project's identifier.
- Filters: `actor` (a member's identifier: their own changes and those of their personal access tokens), `actor_kind`, `action`, `entity_kind`, `entity_id` (which follows an entity whatever key it had), `environment`, `kill_switch` (`true` for the uses of the [kill switch](flags.md#switching-on-and-off-the-kill-switch) alone, `false` for every other entry), `change_request` (a change request's identifier: the entries its [application](change-requests.md#approving-and-applying) wrote, which carry it as `change_request_id`), `from` and `to` (both included), and `q`, text found in the action, the entity's key or the actor's name, whatever its case.
- Newest first, 50 entries a page unless `limit` asks for up to 200; the answer's `next_cursor` fetches the next page.

The console shows both logs, the project's under **Audit log** in the project navigation and the instance's under **Audit log** in the instance navigation, with these filters and each entry's values before and after.
