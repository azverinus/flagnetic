// Package infra is the root of the infrastructure layer.
//
// Inbound adapters under infra/in translate requests (HTTP, SSO callbacks,
// worker ticks, CLI) into application commands and queries and hold no rules.
// Outbound adapters under infra/out implement application ports: PostgreSQL
// repositories, mail, ClickHouse, object storage, outbound HTTP.
package infra
