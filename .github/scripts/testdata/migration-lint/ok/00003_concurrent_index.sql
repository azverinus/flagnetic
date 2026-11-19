-- +goose NO TRANSACTION
-- +goose Up
-- The index shape the rule asks for: built CONCURRENTLY outside goose's
-- transaction, which the lint must still accept although it assumes every
-- other file runs in one.
CREATE INDEX CONCURRENTLY IF NOT EXISTS flags_owner ON flags (owner);
