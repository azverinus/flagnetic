# Provisioning members with SCIM

An identity provider (Microsoft Entra ID, Okta, or any SCIM 2.0 client) can create, update and deprovision the instance's members through `/scim/v2`, so that people arrive when the company hires them and lose their access when it lets them go, without an instance admin noticing in between. The server speaks SCIM 2.0 ([RFC 7643](https://www.rfc-editor.org/rfc/rfc7643) and [RFC 7644](https://www.rfc-editor.org/rfc/rfc7644)) for **Users** and **Groups**, and the provider's [group mappings](sso.md#group-mappings) turn the groups into roles.

Provisioning needs an [auth provider](sso.md) for the same identity provider: the members it provisions sign in through it, and a SCIM token belongs to it.

## SCIM tokens

A SCIM token is the credential the identity provider presents in `Authorization: Bearer`. An instance admin creates one, in a signed-in session (`403 session_required` for any API token), with `POST /instance/scim-tokens`:

```json
{ "provider": "entra", "expires_at": "2027-11-06T00:00:00Z" }
```

`provider` is the slug of the auth provider the token is bound to, **for life**; `expires_at` is optional and must lie in the future. The answer carries the token's `value`, `fn_scim_` and 40 characters, **shown this once**; afterwards the token is known by its hint (`fn_scim_Ab3d`), its provider, its expiry and when it was last presented, recorded at most once a minute. `GET /instance/scim-tokens` lists them, revoked and expired ones included, and `DELETE /instance/scim-tokens/{token}` revokes one. Creating and revoking are audited as `scim_token.created` and `scim_token.revoked`, naming the hint and the provider and never the value.

Several tokens may coexist, which is how a token is rotated: create the new one, give it to the identity provider, revoke the old one.

A token is refused `401 invalidCredentials` when it is unknown, revoked or expired, when its provider is **disabled** (enabling the provider brings its tokens back), and when what is presented is no SCIM token at all, a personal access token or a project token included. The answers are alike, so a caller cannot tell which. Every refusal counts against the throttle of the address it comes from, the one local sign-in, invitation acceptance and password reset share: an address past 100 failures in 15 minutes is answered `429` with `Retry-After`. A token that is accepted costs nothing against it. Tokens are stored sealed under the instance's secret key and compared one by one, so an instance is meant to hold a handful.

## What a token reaches

A token reaches **its provider's members and groups, and nothing else**. A member the provider provisioned or adopted is a *provisioned member*; another provider's member, and a member nobody provisioned, are not found (`404`) through it, and are not in its lists or filters. An email held by a member it cannot take over is `409 uniqueness`, never an answer that tells whose member it is. An `externalId` is unique among one provider's members, and free for another's. A group's `displayName` and `externalId` are likewise unique among one provider's groups: two providers may each hold a group called `engineering`, and each provider's group mappings see its own alone.

## Setting up Microsoft Entra ID

1. In Flagnetic, [add an auth provider](sso.md) for the Entra application and create a SCIM token for it.
2. In the Entra application, open **Provisioning**, set the mode to **Automatic**, and enter the **Tenant URL** `https://<your instance>/scim/v2` and the token as the **Secret Token**. **Test Connection** reads `GET /Users?filter=userName eq "…"`.
3. Under **Mappings**, keep `userName` (from the user principal name or mail), `active`, `displayName`, `emails[type eq "work"].value` and `externalId`. `externalId` is what links a sign-in (below), so map it to the attribute the sign-in carries as its `sub` or NameID: for OIDC the object ID, for SAML what the `NameID` holds. Attributes the instance does not map (`title`, phone numbers, the enterprise extension) are refused `400 noTarget` in a patch: remove them from the mappings.
4. To push groups, keep the group mappings of **Provisioning** on, with `displayName`, `externalId` and `members`, and assign the groups to the application. The group's `displayName` is what the provider's [group mappings](sso.md#group-mappings) name.
5. Switch provisioning **On**.

Entra sends `active` as the strings `"True"` and `"False"`, and the operations `Add`, `Replace` and `Remove`; the server reads both.

## Setting up Okta

1. In Flagnetic, add an auth provider for the Okta application and create a SCIM token for it.
2. In the Okta application, **Provisioning** → **Integration**: enable SCIM, set the **SCIM connector base URL** to `https://<your instance>/scim/v2`, the **Unique identifier field for users** to `userName`, check **Push New Users**, **Push Profile Updates** and **Push Groups**, choose **HTTP Header** authentication and paste the token.
3. Under **To App**, enable **Create Users**, **Update User Attributes** and **Deactivate Users**.
4. Under **Push Groups**, push the groups whose members should receive roles. Their names are what the provider's group mappings name.

Okta deactivates a user with a patch that has no path (`{"op": "replace", "value": {"active": false}}`), renames a group with one that also repeats the group's `id`, and removes a member with the path `members[value eq "<id>"]`; the server reads these shapes too.

## Users

`/scim/v2/Users` answers `application/scim+json` and errors as RFC 7644 §3.12 describes them: `schemas`, `status` as a string, `detail`, and a `scimType` where the RFC names one (`invalidFilter`, `invalidSyntax`, `invalidValue`, `mutability`, `uniqueness`, `noTarget`). Bodies are at most 64 KiB (`413`). A User is:

| Attribute | Meaning |
|---|---|
| `id` | The member's identifier. |
| `userName`, `emails` | The member's email. `userName` wins over `emails` when both are sent and differ; without it the primary email, else the first. |
| `displayName`, `name` | The member's name: `displayName`, else `name.givenName` and `name.familyName` joined, else the email. |
| `externalId` | The directory's own identifier of the person. Unique among the provider's members. |
| `active` | Whether the member can sign in. |
| `groups` | The provider's [groups](#groups) the member is in, each as `value` (the group's `id`), `display` (its name) and `$ref`; read only. |

`ServiceProviderConfig`, `ResourceTypes` and `Schemas` describe the server, Users and Groups: patch and filter are supported (at most 100 results), bulk, sort and password changes are not. `ResourceTypes` and `Schemas` are list responses.

- **`POST /Users`** creates an **active** member of the token's provider, with no password: the provider is how they sign in. Audited `member.provisioned`. A user sent with `active: false` is created and then deactivated.
- **`GET /Users/{id}`** reads one.
- **`GET /Users`** lists and filters. A filter compares attributes with `eq`, alone or joined by `and`: `userName`, `emails.value`, `externalId`, `displayName` and `id`; `userName`, `emails.value` and `displayName` ignore case. Anything else (`or`, `not`, `co`, `pr`, another attribute) is `400 invalidFilter`. `startIndex` is 1-based; `count` is from 0 to 100 (100 by default, higher capped), and 0 answers the total alone. Members are ordered by email.
- **`PUT /Users/{id}`** replaces the email, the name and, when sent, the `externalId`; when `active` is sent it deactivates or reactivates. An `externalId` that is not sent is left as it is.
- **`PATCH /Users/{id}`** applies `add`, `replace` and `remove` operations, in order, as one change. An operation names its attribute in `path` (`active`, `userName`, `emails`, `emails[type eq "work"].value`, `displayName`, `externalId`) or, with no path, in an object value. `name` and its parts are accepted and change nothing: the member has one name, `displayName`. `remove` empties `displayName` (the member is named after their email again) and `externalId`; `userName`, `emails` and `active` cannot be removed. `groups`, `id` and `meta` are read only (`400 mutability`), any other attribute is `400 noTarget`. A patch that fails changes nothing.
- **`DELETE /Users/{id}`** deactivates the member and answers `204`. No member is ever deleted.

A change that alters the member is audited `member.updated`, with what changed, and one that alters nothing is not. Every write names the SCIM token as its actor (`scim`, the token's id, its hint).

### Adoption

An instance that already has members can switch SCIM on without duplicating them: `POST /Users` for an email a member already holds **adopts** that member when all of these hold, and is otherwise `409 uniqueness`:

- the email is in one of the provider's [domains](sso.md#domains-linking-and-provisioning);
- the member is **active** (an invited member has an invitation to accept, a deactivated one was offboarded by someone);
- the member is **no instance admin**;
- no provider provisions the member already.

An adopted member keeps their identifier, their grants, their password (if they have one) and their sessions; from then on they belong to the provider, and its directory decides their email, name and status. Audited `member.adopted`.

### Deprovisioning

`active: false` and `DELETE` deactivate the member in the transaction the write commits in, so the member has lost everything before the response is sent: every session and personal access token is revoked, their open invitations and password resets end, and the invitations they issued lose their grants, as when an instance admin deactivates a member. Audited `member.deactivated` with the number of sessions and tokens revoked. The last active instance admin is not deactivated (`409 last_instance_admin`). The provider's [group mappings](#groups-and-group-mappings) are run for the member in the same transaction, so a deactivated member keeps none of the grants the provider's mappings made. `active: true` reactivates the member as active, since the provider is how they sign in, and runs the mappings again, so they regain what their groups map to; what the deactivation revoked otherwise stays revoked.

### Provisioned members

A provisioned member's **email, name and status follow the directory**, so the management API refuses to change them by hand: editing the email or name (`PATCH /members/{member}`, or the member's own `PATCH /me` for the name), deactivating and reactivating are `409 member_provisioned`. Everything else stays the instance's: grants, the instance admin switch, the member's language, their personal access tokens. `GET /members` answers `provider`, the slug of the member's provider, or `null`.

## Groups

`/scim/v2/Groups` holds the groups of the token's provider. A Group is:

| Attribute | Meaning |
|---|---|
| `id` | The group's identifier. |
| `displayName` | The group's name, at most 256 characters; required, and unique among the provider's groups. The provider's group mappings match it exactly. |
| `externalId` | The directory's own identifier of the group, optional. Unique among the provider's groups. |
| `members` | The members in the group, each as `value` (the User's `id`) and `$ref`. Only Users the provider provisions join a group. |

- **`POST /Groups`** creates a group, with its members. A member that is not one of the provider's Users is `400 invalidValue`, whether it exists elsewhere or not; a taken `displayName` or `externalId` is `409 uniqueness`. Audited `scim_group.created`.
- **`GET /Groups/{id}`** reads one, and **`GET /Groups`** lists and filters. A filter compares `displayName`, `externalId` or `id` with `eq`, each exactly, alone or joined by `and`; anything else is `400 invalidFilter`. Paging works as for Users; groups are ordered by name. `excludedAttributes=members` leaves the members out, as both directories ask when they look a group up.
- **`PUT /Groups/{id}`** replaces the name and the members (none when `members` is not sent), and the `externalId` when it is sent.
- **`PATCH /Groups/{id}`** applies `add`, `replace` and `remove` operations, in order, as one change, in the shapes Entra ID and Okta send:
  - `add` on `members` with a list of `{"value": "<id>"}` adds them (a member already in is one member), `remove` with a list removes them, and `remove` without a value empties the group; `replace` makes the list the group's members;
  - `remove` on `members[value eq "<id>"]` removes that member; removing someone who is not a member changes nothing;
  - `add` or `replace` on `displayName` and `externalId`, by path or in an object with no path; `remove` empties `externalId`, and the name cannot be removed (`400 invalidValue`). An object with no path may repeat the group's own `id`, as Okta's rename does; another `id`, and `meta`, are read only (`400 mutability`), and any other attribute is `400 noTarget`.
- **`DELETE /Groups/{id}`** deletes the group and answers `204`; its members leave it.

A change that alters the group is audited `scim_group.updated`, with the name and `externalId` when they changed and the members who joined (`members_added`) and left (`members_removed`); one that alters nothing is not. A deleted group is audited `scim_group.deleted` with what it held. Every write names the SCIM token as its actor.

### Groups and group mappings

A provider's [group mappings](sso.md#group-mappings) name groups, and a member's groups at the provider are the provider's SCIM groups they are in together with the groups their last sign-in through it claimed. Every change of a group runs the provider's mappings again, in the transaction the change commits in, for each member it touches: those who join or leave, and every member when the group is renamed or deleted. The grants and the instance admin that their groups map to are made, and the provider's others removed, recorded with the SCIM token as actor (`grant.created`, `grant.deleted`, `member.promoted`, `member.demoted`, with `source` `sso` and the provider). So a member leaves a mapped group and loses its role at once, without signing in; a sign-in keeps what a SCIM group grants, and a SCIM change what the claim grants. A group no mapping names is kept and grants nothing. Replacing the mappings runs them for every member in one of the provider's groups as well. A group reaches only its own provider's mappings: a token of one provider cannot make an instance admin through another provider's mapping, whatever its groups are called.

## Signing in

A provisioned member signs in through their provider, matched by `externalId`: the first sign-in at the provider whose subject (the OIDC `sub`, the SAML `NameID`) equals the member's `externalId` is linked to that member and signs them in, whatever email the sign-in carries, and with `link_by_email` and `auto_provision` both off. The link is made at the member's provider only: another provider's sign-in with the same subject finds no member. A provisioned member who was deactivated is refused `member_deactivated`.

## Deleting a provider

A provider with a SCIM token that has not been revoked, or with provisioned members, is not deleted (`409 provider_in_use`, `used_by` naming `SCIM tokens` and `provisioned members`): disable it instead, which refuses its SCIM tokens along with its sign-ins and leaves its members as they are.
