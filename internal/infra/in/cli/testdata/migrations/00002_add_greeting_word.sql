-- +goose Up
ALTER TABLE greeting ADD COLUMN word text NOT NULL DEFAULT 'hello';
