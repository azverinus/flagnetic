# Access

Who may do what in a Flagnetic instance: permissions, the roles that list them, the grants that give a member a role in a project, and the API tokens scripts and pipelines use.

## Permissions

Every operation of the management API declares the one thing it needs in the OpenAPI document (`x-permission`): a permission of the catalog, judged in the project the operation addresses, or one of `public` (anyone), `member` (any credential acting for a member) and `instance_admin`. A permission is named `entity.verb`.

| Permission | Allows | Token scope |
|---|---|---|
| `project.read` | every ordinary read in a project, its audit log included | `read` |
| `project.update` | renaming a project and changing its description | `project:admin` |
| `environment.manage` | creating, renaming and deleting environments | `project:admin` |
| `declaration.manage` | declaring, renaming and deleting unit types and attributes | `flags:write` |
| `grant.manage` | making and removing grants | `project:admin` |
| `project_token.manage` | listing, creating and revoking project tokens | `project:admin` |

A refusal for a missing permission answers `403 forbidden` and names the permission in the problem's `permission` member, so a script can ask for exactly that. An unknown project answers `404 not_found` before any permission is judged.

## Roles

A role is a fixed list of permissions. The four built-in roles are the same on every instance:

| Permission | viewer | developer | maintainer | owner |
|---|---|---|---|---|
| `project.read` | ✓ | ✓ | ✓ | ✓ |
| `project.update` | | | | ✓ |
| `environment.manage` | | | ✓ | ✓ |
| `declaration.manage` | | | ✓ | ✓ |
| `grant.manage` | | | | ✓ |
| `project_token.manage` | | | | ✓ |

`GET /roles` lists every role with its permissions, so a client can explain which roles hold a permission a caller lacks. Later releases add permissions as features arrive and write them into the built-in roles.

## Grants

A grant is one role held by one member in one project. A project owner grants a member one or more roles (`POST /projects/{project}/grants`) and removes one grant at a time, leaving the others (`DELETE /projects/{project}/grants/{grant}`). A member's permissions in a project are the union of their grants' roles there, so holding more grants never takes anything away. `GET /projects/{project}/members` lists the members holding grants with their grants side by side, and `GET /projects/{project}/me` answers the caller's grants and effective permissions there.

- The same role twice for the same member is refused `409 already_granted`; a deactivated member `409 member_deactivated`. An invited member may be granted roles before they join.
- Nobody makes or removes a grant of a role holding a permission they lack in the project (`422 role_not_allowed`), so managing access never escalates into more access. With the built-in roles only owners manage grants, and they hold every permission of a project.
- An instance admin holds every permission in every project without a grant, and is every project's backstop: a project's creator receives no grant, and removing the last owner's grant is allowed.
- A member sees in `GET /projects` the projects where they hold a grant; an instance admin sees every project.

## API tokens and scopes

A script authenticates with an API token in the `Authorization` header as a bearer. A token carries scopes, which only narrow what it may do: each permission falls under one scope, and a token exercises a permission only when it carries that scope. The scopes are `read`, `flags:write`, `experiments:write`, `project:admin` and `instance:admin`; a token carries `read` alone unless asked for more, and `read` beside every write scope. A scope the token lacks is refused `403 insufficient_scope` before its project is looked up. The operations of the instance (`instance_admin`) need an instance admin, whose token carries `instance:admin` to change anything there and `read` to read. A session is narrowed by no scope.

A token's expiry lies in the future and at most the instance's maximum token lifetime ahead (365 days on a fresh instance); an instance admin can lower that lifetime or require every token to expire in the auth settings. Without an expiry a token never expires. An expired token is refused `401 token_expired`, a revoked one `401 token_revoked`, an unknown one `401 invalid_token`, each named in `WWW-Authenticate`. A token's value is shown once, when it is created; afterwards only its hint (its prefix and first four characters) and when and from where it was last used, recorded at most once a minute.

### Personal access tokens

A personal access token (`fn_pat_…`) acts for the member who created it, with that member's permissions narrowed by its scopes. A member creates, lists and revokes their own under `/me/tokens`; `instance:admin` is for an instance admin alone (`422 scope_not_allowed`). An instance admin lists and revokes any member's under `/members/{member}/tokens`. Deactivating a member revokes every personal access token they hold.

### Project tokens

A project token (`fn_prj_…`) belongs to a project rather than to a person, for a pipeline. An owner creates it holding one built-in role in the project, fixed for its life (`/projects/{project}/tokens`). Its permissions are its role's narrowed by its scopes, minus `grant.manage` and `project_token.manage`: whatever its role, a project token never changes who has access to the project. It reaches its own project and `GET /me` alone (`403 forbidden` anywhere else, the project list included), never carries `instance:admin`, and keeps working when the member who created it leaves.

### Only a session creates a credential

Creating a personal access token or a project token, and every write to a member's own account (their name, password, sessions and tokens), needs a signed-in session: any API token is refused `403 session_required`, however wide its scopes. A leaked token therefore can neither mint another one that outlives its expiry or its revocation, nor sign its owner out or hide its siblings. The cost is that a script cannot rotate its own token: a person does it, signed in, which is when someone should look at what the script holds. `flagnetic admin bootstrap` is the one other way to a token, since it runs with the database's own credentials.
