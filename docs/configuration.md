# Configuration reference

<!-- Generated from the settings table in internal/infra/config by `task generate`. Do not edit. -->

Flagnetic is configured with environment variables and, optionally, a YAML file named by `FLAGNETIC_CONFIG_FILE`. Each setting is read from its environment variable when that is set, even to the empty string; otherwise from the file; otherwise it takes its default. An empty value means the default, except where a setting says it switches something off.

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
| `FLAGNETIC_METRICS_LISTEN_ADDR` | `metrics_listen_addr` | `:9090` | Address of the metrics listener, which serves Prometheus metrics on `/metrics` and nothing else. Keep it off the internet. Set it empty to switch metrics off and open no port. Example: `127.0.0.1:9090`. |
| `FLAGNETIC_AUTO_MIGRATE` | `auto_migrate` | `true` | Whether `serve` applies pending migrations on start. Set it to false when migrations run as a job (`flagnetic migrate`). Example: `false`. |
| `FLAGNETIC_DB_MAX_CONNS` | `db_max_conns` | `10` | Size of the PostgreSQL connection pool of one replica. When unset, `pool_max_conns` in `DATABASE_URL` applies if present; when both are set, this wins and a notice is logged. Unit: connections. Example: `25`. |
| `FLAGNETIC_SHUTDOWN_DRAIN` | `shutdown_drain` | `5s` | How long a replica told to stop keeps serving while `/readyz` answers 503, so load balancers route away from it before it stops accepting requests. `0` skips the drain. Unit: duration. Example: `5s`. |
| `FLAGNETIC_SHUTDOWN_GRACE` | `shutdown_grace` | `15s` | How long in-flight requests may run once the drain is over and the replica stops accepting new ones; then they are cancelled. Unit: duration. Example: `15s`. |
| `FLAGNETIC_LOG_LEVEL` | `log_level` | `info` | Lowest level logged: debug, info, warn or error. Example: `debug`. |
| `FLAGNETIC_LOG_FORMAT` | `log_format` | `json` | Format of the log written to standard error: json, one object per line, or text. Example: `text`. |
| `FLAGNETIC_TRUSTED_PROXIES` | `trusted_proxies` | — | Comma-separated CIDR prefixes or single addresses of the reverse proxies in front of the server. Their `X-Forwarded-For` and `X-Forwarded-Proto` headers are believed, so the client's address and scheme are the real client's; from any other peer the headers are ignored, so a client cannot choose the address it is seen with. Example: `10.0.0.0/8,192.168.1.10`. |
| `FLAGNETIC_HTTP_READ_HEADER_TIMEOUT` | `http_read_header_timeout` | `10s` | How long a client may take to send a request's headers on the public listener. Unit: duration. Example: `10s`. |
| `FLAGNETIC_HTTP_IDLE_TIMEOUT` | `http_idle_timeout` | `120s` | How long a keep-alive connection may stay idle before the server closes it. Unit: duration. Example: `120s`. |
| `FLAGNETIC_HTTP_BODY_READ_TIMEOUT` | `http_body_read_timeout` | `30s` | How long a client may take to send a request's body; a body still arriving then is answered 408. Long-lived streams are excepted. Unit: duration. Example: `30s`. |
| `FLAGNETIC_HTTP_MAX_BODY_BYTES` | `http_max_body_bytes` | `1048576` | Largest request body accepted, unless a route declares a larger limit of its own; a larger body is answered 413. Unit: bytes. Example: `1048576`. |
