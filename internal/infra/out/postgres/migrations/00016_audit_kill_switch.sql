-- +goose Up

-- Marks the entries recording a kill switch, so an auditor reviews every
-- emergency switch-off of a flag in one list. They are few among all the
-- entries, so a partial index ordered like the log answers that list
-- without growing with the rest.
ALTER TABLE audit_entries ADD COLUMN kill_switch boolean NOT NULL DEFAULT false;

CREATE INDEX audit_entries_kill_switch ON audit_entries (id DESC) WHERE kill_switch;
