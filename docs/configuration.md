# Configuration reference

<!-- Generated from the settings table in internal/infra/config by `task generate`. Do not edit. -->

Flagnetic is configured with environment variables and, optionally, a YAML file named by `FLAGNETIC_CONFIG_FILE`. Each setting is read from its environment variable when that is set, even to the empty string; otherwise from the file; otherwise it takes its default. An empty value means the default.

The file is a mapping from the keys in the File key column to single values:

```yaml
database_url: postgres://flagnetic@db:5432/flagnetic?sslmode=require
listen_addr: ":8080"
log_format: text
```

A file key that is not listed here, a value that is not a single value, and a value that does not parse are refused when the server starts, with exit code 2 and a message naming the setting.

Durations are written as a number and a unit: `500ms`, `15s`, `2m`.

| Variable | File key | Default | Description |
|---|---|---|---|
| `DATABASE_URL` | `database_url` | required | PostgreSQL connection string, as a URL or as keyword/value pairs. PostgreSQL 16 or newer. Example: `postgres://flagnetic@db:5432/flagnetic?sslmode=require`. |
| `FLAGNETIC_CONFIG_FILE` | — | — | YAML file of settings, keyed by the names in the File key column. Environment only. Example: `/etc/flagnetic/flagnetic.yaml`. |
| `FLAGNETIC_LISTEN_ADDR` | `listen_addr` | `:8080` | Address of the public listener: the APIs, the console, `/healthz` and `/readyz`. Example: `:8080`. |
| `FLAGNETIC_AUTO_MIGRATE` | `auto_migrate` | `true` | Whether `serve` applies pending migrations on start. Set it to false when migrations run as a job (`flagnetic migrate`). Example: `false`. |
| `FLAGNETIC_DB_MAX_CONNS` | `db_max_conns` | `10` | Size of the PostgreSQL connection pool of one replica. When unset, `pool_max_conns` in `DATABASE_URL` applies if present; when both are set, this wins and a notice is logged. Unit: connections. Example: `25`. |
| `FLAGNETIC_SHUTDOWN_GRACE` | `shutdown_grace` | `15s` | How long in-flight requests may run once the replica stops accepting new ones; then they are cancelled. Unit: duration. Example: `15s`. |
| `FLAGNETIC_LOG_LEVEL` | `log_level` | `info` | Lowest level logged: debug, info, warn or error. Example: `debug`. |
| `FLAGNETIC_LOG_FORMAT` | `log_format` | `json` | Format of the log written to standard error: json, one object per line, or text. Example: `text`. |
