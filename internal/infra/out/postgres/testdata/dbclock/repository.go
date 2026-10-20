package dbclock

// A comment naming now() is not SQL.
const expire = `DELETE FROM session WHERE expires_at < Now()`
