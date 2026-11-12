import type { Sink } from "@flagnetic/api";

/** A stored ClickHouse sink, as the server answers one. */
export const clickhouseSink: Sink = {
  id: "0192a000-0000-7000-8000-0000000000c1",
  name: "Warehouse",
  kind: "clickhouse",
  clickhouse: {
    url: "https://ch.example.com:8443",
    database: "analytics",
    table_prefix: "flagnetic",
    username: "writer",
    password: "***",
    tls_skip_verify: false,
    create_tables: true,
    settings: { max_threads: "4" },
  },
  event_kinds: [],
  environments: [],
  batch_size: 5000,
  flush_interval: 10,
  enabled: false,
  health: "disabled",
  at_risk: false,
  lag_seconds: null,
  dead_letters: 0,
  gaps: 0,
  stopped_at: null,
  warnings: [],
  streams: [],
  created_at: "2026-11-01T10:00:00Z",
  updated_at: "2026-11-01T10:00:00Z",
  version: 1,
};

export const objectStorageSink: Sink = {
  ...clickhouseSink,
  id: "0192a000-0000-7000-8000-0000000000c2",
  name: "Lake",
  kind: "object_storage",
  clickhouse: undefined,
  object_storage: {
    endpoint: "",
    region: "eu-west-1",
    bucket: "events",
    prefix: "flagnetic",
    access_key_id: "AKIA",
    secret_access_key: "***",
    path_style: false,
    format: "jsonl",
    compression: "gzip",
  },
  batch_size: 500000,
  flush_interval: 300,
};

export const webhookSink: Sink = {
  ...clickhouseSink,
  id: "0192a000-0000-7000-8000-0000000000c3",
  name: "Pipeline",
  kind: "webhook",
  clickhouse: undefined,
  webhook: { url: "https://pipeline.example.com/in", headers: { Authorization: "Bearer t" } },
  batch_size: 500,
  flush_interval: 5,
};
