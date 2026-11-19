-- +goose Up
-- 'now', 'today' and now() in a comment read nothing.
CREATE TABLE special (
    a timestamptz NOT NULL DEFAULT 'now',
    b timestamptz NOT NULL DEFAULT 'tomorrow'::timestamptz,
    c date NOT NULL DEFAULT ' Today ',
    d text NOT NULL DEFAULT timeofday()
);
SELECT 'yesterday'::date; -- a comment after the read
SELECT '--' || now(); /* a literal holding the comment opener hides nothing */
SELECT '/*' || 'not a comment', 'it''s' || 'fine', "now" FROM special;
