# Governance

Governance is how a team controls changes to what its SDKs receive: which environments need a second pair of eyes, and how a change reaches them. This page covers protected environments; [change requests](change-requests.md) covers proposing changes.

## Protected environments

An environment is protected when what its SDKs receive should change only through a reviewed change request. An owner protects an environment or stops protecting it with `PUT /projects/{project}/environments/{environment}/protection` and `{"protected": true}` or `false`, under `If-Match` on the environment's version; it needs `environment.protect`, which only owners hold and no project token exercises, so a pipeline cannot unprotect an environment, write it and protect it again. Asking for the protection an environment already has changes nothing. Each change is recorded in the audit log as `environment.protected` or `environment.unprotected`, and `protected` is part of every environment the API answers.

The production environment of a project created from this release on starts protected; the other environments, and every environment that existed before the upgrade, start unprotected, so an upgrade does not stop a script writing production directly. Protect production yourself where you want its changes reviewed.

SDKs read the protection from their ruleset and evaluate answers (`environment.protected`), and a server SDK ignores local overrides in a protected environment. Protecting or unprotecting an environment moves its ruleset version, so SDKs learn it at once.

### What protection refuses

A write that would change what a protected environment's SDKs receive is refused `403 change_request_required`, whatever it touches and whichever endpoint it goes through: a flag's configuration there (replacing it, an override, a rollout's salt, a revert, a copy into it), a flag's variations or automatic exposure, a segment's conditions or private switch when a flag uses the segment there, archiving a flag switched on there or restoring one that was. The refusal's `detail` names the protected environments the write would change. Nothing of the write is kept: a write that would change a protected environment and others too, such as editing a segment that staging and production both use, changes none of them.

Protection judges the result, not the endpoint. When a write is about to commit, the server builds each protected environment's ruleset as the write would leave it and compares it with the ruleset that environment serves now. It leaves out what no evaluation depends on, so these writes stay direct in a protected environment:

- a flag's name, description, tags, owner and lifetime, and creating a flag;
- archiving or restoring a flag that is off in the environment;
- creating a segment, and editing or deleting one no flag in the environment uses;
- declaring, editing and removing unit types and attributes;
- an environment's name and order, and its protection itself;
- any write to an environment that is not protected.

A segment counts only when a flag's rule uses it both before and after the write; a rule that starts or stops using a segment is a change of that flag. A flag that is off in the environment still counts in full: its rules and its variations' values are what switching it on would serve.

A request is checked in order: its permission first, then whether it is valid, then `If-Match`, and protection last. A write that is malformed or based on an old version says so before it is told to become a change request.

### The kill switch

The kill switch is the one direct write to a protected environment: `POST /flags/{flag}/environments/{environment}/kill` switches the flag off there with the permission it always needs (`flag_configuration.kill`, developer and above), so whoever is on call stops an incident without waiting for an approval. It is audited as every kill switch is, marked `kill_switch`. Switching the flag back on is an ordinary configuration write, which a protected environment takes only through a change request.

### What protection costs

A write that changes no ruleset pays nothing. A write that changes a ruleset pays, for each protected environment it reaches, one build of that environment's ruleset inside its transaction, compared with the ruleset the replica already holds in memory. When the replica does not hold it yet, it reads it on two PostgreSQL connections of its own, outside its pool, so writes waiting for each other never wait for a connection too. `flagnetic_protection_guard_seconds` measures it per write (see [Operations](operations.md#metrics)).
