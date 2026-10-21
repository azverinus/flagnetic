-- +goose Up

-- The member who created a project token; null for a personal access
-- token, which acts for its member. A project token outlives its creator:
-- it belongs to its project.
ALTER TABLE api_tokens ADD COLUMN created_by uuid REFERENCES members (id);

CREATE INDEX api_tokens_project_idx ON api_tokens (project_id) WHERE project_id IS NOT NULL;
