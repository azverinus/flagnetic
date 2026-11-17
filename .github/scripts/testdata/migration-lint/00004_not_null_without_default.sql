-- +goose Up
-- Breaks a replica of the previous minor or blocks it, so the lint must fail
-- on it. It lives outside the migrations directory and is never applied.
ALTER TABLE flags ADD COLUMN owner text NOT NULL;
