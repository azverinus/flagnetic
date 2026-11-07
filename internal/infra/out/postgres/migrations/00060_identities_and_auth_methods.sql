-- +goose Up

-- A member links an auth provider while signed in: the attempt is bound to
-- the member and to the session that started it, and completing it needs that
-- session.
ALTER TABLE sso_attempts DROP CONSTRAINT sso_attempts_intent_check;
ALTER TABLE sso_attempts ADD CONSTRAINT sso_attempts_intent_check CHECK (intent IN ('login', 'link'));
ALTER TABLE sso_attempts ADD COLUMN member_id  uuid REFERENCES members (id);
ALTER TABLE sso_attempts ADD COLUMN session_id uuid REFERENCES sessions (id) ON DELETE CASCADE;
ALTER TABLE sso_attempts ADD CONSTRAINT sso_attempts_link_binding CHECK (
    (intent = 'link') = (member_id IS NOT NULL) AND (intent = 'link') = (session_id IS NOT NULL)
);

-- When the member set the password they hold, to show it in their sign-in
-- methods. The updated_at of a member moves with every edit, so it cannot
-- tell.
ALTER TABLE members ADD COLUMN password_set_at timestamptz;
UPDATE members SET password_set_at = updated_at WHERE password_hash IS NOT NULL;
ALTER TABLE members ADD CONSTRAINT members_password_set_at CHECK ((password_hash IS NULL) = (password_set_at IS NULL));
