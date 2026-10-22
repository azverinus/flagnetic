-- +goose Up

-- Every replica keeps the rulesets of the environments its SDKs ask for,
-- with what it learned of their versions. Deleting an environment, a
-- cascade from its project included, notifies its identifier alone on
-- rulesets once the transaction commits, and every replica forgets the
-- environment: its cached ruleset and its metric series. A new version is
-- notified by the statement that bumps it, with the version.
-- +goose StatementBegin
CREATE FUNCTION environments_notify_deleted() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('rulesets', OLD.id::text);
    RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER environments_deleted
    AFTER DELETE ON environments
    FOR EACH ROW
    EXECUTE FUNCTION environments_notify_deleted();
