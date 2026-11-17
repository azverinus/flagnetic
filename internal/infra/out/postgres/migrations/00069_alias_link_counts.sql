-- +goose Up

-- How many alias links each environment holds, kept by the writes that add
-- them: counting the links themselves would read every one of them on each
-- batch of aliases, which is what the limit on them exists to keep small.
CREATE TABLE alias_link_counts (
    environment_id uuid   PRIMARY KEY REFERENCES environments (id) ON DELETE CASCADE,
    links          bigint NOT NULL CHECK (links >= 0)
);

INSERT INTO alias_link_counts (environment_id, links)
SELECT environment_id, count(*) FROM alias_links GROUP BY environment_id;
