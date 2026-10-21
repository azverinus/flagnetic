-- +goose Up

-- The flags list filters by a tag (tags @> ARRAY[tag]); the GIN index
-- answers it without reading every flag of the project.
CREATE INDEX flags_tags ON flags USING gin (tags);
