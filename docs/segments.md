# Segments

A **segment** is a reusable, named set of conditions on context attributes, such as "customers on a paid plan in Europe". A rule names it with an `in_segment` or `not_in_segment` condition instead of repeating its conditions, and every rule naming it follows when it changes. Segments live under `/api/v1/projects/{project}/segments`; editing one needs `segment.edit` and deleting one `segment.delete` (see [Access](access.md#permissions)).

## What a segment holds

| Field | Rule |
|---|---|
| `key` | A lowercase letter, then lowercase letters, digits, `-` and `_`, at most 64 in all. Unique in the project and never changes. |
| `name`, `description` | A name of 1 to 200 characters, a description of at most 2,000. |
| `private` | Whether it is a **private segment** (below). |
| `conditions` | At most 20 attribute conditions, all of which must hold for a context to be in the segment. |

A segment holds attribute conditions only: each names an attribute or unit type the project declares, an operator defined for its type and a value that operator accepts, as [Targeting](targeting.md) lists them. Segments do not nest, and do not name flags: a condition of kind `in_segment`, `not_in_segment` or `prerequisite` in a segment is refused. A segment with no conditions holds every context.

`POST /segments` creates one (`409 already_exists` for a key in use), and `PATCH /segments/{segment}` changes its name, description, private switch or conditions, which are replaced as a list; sending a new key is `immutable`. What does not fit is refused `422 invalid_segment`, each violation pointing at its field.

## Private segments

A private segment's conditions never leave the server: only server-side evaluation uses it. A client SDK key learns the value a rule naming it serves to a context the client sends, and nothing about which rule served it or why.

## Where a segment is used

`GET /segments` lists the project's segments by key, each with a `usage_count`: how many flags have a rule naming it, archived flags included, each flag counted once whatever its environments. `GET /segments/{segment}/usages` lists every flag and environment whose rules name it.

A rule naming a segment that does not exist is refused `unknown_segment`. A segment that a rule still names, an archived flag's included, is not deleted (`409 in_use`, `references` naming each flag and environment), since the rule would name nothing and a restored flag would come back broken.

## Changes and history

Every segment carries a `version` answered as an `ETag`; changing or deleting one names the version it was based on in `If-Match` and is refused `428 precondition_required` without it and `412 version_mismatch` once someone else changed it, as for flags (see [Editing at the same time](flags.md#editing-at-the-same-time)). Creating or deleting a segment, and changing its conditions or private switch, moves the ruleset of every environment of the project on; a new name or description does not. Each accepted write records `segment.created`, `segment.updated` or `segment.deleted` with the segment before and after it.

## In the console

**Segments** in the project navigation lists the project's segments with whether each is private, how many conditions it holds and how many flags use it, filtered by text in the key or name and by the private switch, the filters kept in the address. **New segment**, offered to a member holding `segment.edit`, creates one with its key, name, description, private switch and conditions; each condition names an attribute or unit type, an operator of its type and a value typed for it, as a rule's conditions do (see [Targeting](targeting.md)). Creating it opens the segment's page.

A segment's page lists every flag and environment whose rules use it, and edits the rest of the segment as a draft: **Save** sends what changed under the version the page was opened at, and while flags use the segment it names them and asks first, since their rules follow the change; leaving with unsaved changes asks first; a refusal is shown on the field or condition it names. If someone saved the segment meanwhile, saving shows who and when with a diff of their change, and offers to reload, losing the edits, or to keep the edits and save them onto the newer version. **Delete**, offered to a member holding `segment.delete`, is refused while a rule names the segment, naming each flag and environment.
