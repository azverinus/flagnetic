-- +goose Up

-- An invitation may bring the invited member into a project: the role it
-- grants once accepted, and the member who issued it, whose grant the
-- acceptance makes. The grant is dropped (project and role cleared) when its
-- inviter can no longer hand it out, while the invitation still activates
-- its member. Deleting the project clears the project; the role of such an
-- invitation grants nothing, since the grant it describes needs a project.
-- Invitations issued before this migration, and those of members reactivated,
-- name no inviter.
ALTER TABLE invitations
    ADD COLUMN project_id uuid REFERENCES projects (id) ON DELETE SET NULL,
    ADD COLUMN role       text CHECK (role IN ('owner', 'maintainer', 'developer', 'viewer')),
    ADD COLUMN invited_by uuid REFERENCES members (id);

-- The open invitations of an inviter, for dropping their grants when the
-- inviter loses the right to hand them out.
CREATE INDEX invitations_inviter ON invitations (invited_by, project_id) WHERE consumed_at IS NULL;
