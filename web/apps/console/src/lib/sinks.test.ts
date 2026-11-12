import { i18n } from "@lingui/core";
import { describe, expect, it } from "vitest";

import { clickhouseSink, objectStorageSink, webhookSink } from "@/test/sinks";

import {
  bodyOf,
  changedFieldNames,
  emptyForm,
  formOf,
  healthName,
  isUnchanged,
  lastDelivery,
  patchOf,
  pointerOfName,
} from "./sinks";

describe("the sink form", () => {
  it("starts a stored sink's secrets empty, so saving keeps them", () => {
    expect(formOf(clickhouseSink).clickhouse.password).toBe("");
    expect(formOf(objectStorageSink).object_storage.secret_access_key).toBe("");
    expect(isUnchanged(clickhouseSink, formOf(clickhouseSink))).toBe(true);
    expect(isUnchanged(objectStorageSink, formOf(objectStorageSink))).toBe(true);
    expect(isUnchanged(webhookSink, formOf(webhookSink))).toBe(true);
  });

  it("creates with the kind's configuration alone and leaves blank batching to the default", () => {
    const form = emptyForm("clickhouse");
    form.name = " Warehouse ";
    form.clickhouse = {
      ...form.clickhouse,
      url: " https://ch.example.com ",
      database: "analytics",
      username: "writer",
      password: "s3cret",
      settings: [
        { name: "max_threads", value: "4" },
        { name: " ", value: "ignored" },
      ],
    };
    expect(bodyOf(form)).toEqual({
      name: "Warehouse",
      kind: "clickhouse",
      clickhouse: {
        url: "https://ch.example.com",
        database: "analytics",
        table_prefix: "",
        username: "writer",
        password: "s3cret",
        tls_skip_verify: false,
        create_tables: true,
        settings: { max_threads: "4" },
      },
      event_kinds: [],
      environments: [],
    });
  });

  it("sends Parquet with no compression of its own", () => {
    const form = formOf(objectStorageSink);
    form.object_storage.format = "parquet";
    const body = bodyOf(form);
    expect(body.object_storage?.compression).toBe("");
    expect(body.object_storage).not.toHaveProperty("secret_access_key");
  });

  it("patches only what changed, and the whole configuration when any of it did", () => {
    const form = formOf(clickhouseSink);
    form.name = "Analytics";
    form.environments = ["production"];
    expect(patchOf(clickhouseSink, form)).toEqual({
      name: "Analytics",
      environments: ["production"],
    });

    form.clickhouse.database = "events";
    const patch = patchOf(clickhouseSink, form);
    expect(patch.clickhouse).toMatchObject({
      database: "events",
      url: "https://ch.example.com:8443",
    });
    expect(patch.clickhouse).not.toHaveProperty("password");
  });

  it("sends a typed secret, and nothing for a secret left empty", () => {
    const form = formOf(objectStorageSink);
    form.object_storage.secret_access_key = "new-key";
    expect(patchOf(objectStorageSink, form).object_storage).toMatchObject({
      secret_access_key: "new-key",
      bucket: "events",
    });
  });

  it("returns cleared batching to the kind's default", () => {
    const form = formOf({ ...webhookSink, batch_size: 100, flush_interval: 30 });
    form.batch_size = "";
    form.flush_interval = "";
    expect(patchOf({ ...webhookSink, batch_size: 100, flush_interval: 30 }, form)).toEqual({
      batch_size: 500,
      flush_interval: 5,
    });
  });

  it("compares event kinds and environments as sets", () => {
    const sink = { ...webhookSink, event_kinds: ["alias", "exposure"] as const };
    const form = formOf({ ...sink, event_kinds: [...sink.event_kinds] });
    form.event_kinds = ["exposure", "alias"];
    expect(isUnchanged({ ...sink, event_kinds: [...sink.event_kinds] }, form)).toBe(true);
  });

  it("escapes a name in a pointer", () => {
    expect(pointerOfName("/webhook/headers", "a/b~c")).toBe("/webhook/headers/a~1b~0c");
  });

  it("names what a newer version changed", () => {
    expect(
      changedFieldNames(i18n, webhookSink, {
        ...webhookSink,
        name: "Other",
        webhook: { url: "https://other.example.com", headers: {} },
        enabled: true,
      }),
    ).toEqual(["Name", "Connection", "Enabled"]);
  });
});

describe("a sink's state", () => {
  it("finds the latest delivery over the streams", () => {
    expect(lastDelivery(webhookSink)).toBeNull();
    expect(
      lastDelivery({
        ...webhookSink,
        streams: [
          { event_kind: "exposure", last_delivered_at: "2026-11-02T10:00:00Z", last_failure: null },
          { event_kind: "alias", last_delivered_at: "2026-11-03T10:00:00Z", last_failure: null },
          { event_kind: "evaluation_summary", last_delivered_at: null, last_failure: null },
        ],
      }),
    ).toBe("2026-11-03T10:00:00Z");
  });

  it("names every health", () => {
    for (const health of [
      "disabled",
      "stopped",
      "failing",
      "degraded",
      "lagging",
      "healthy",
    ] as const) {
      expect(healthName(i18n, health)).not.toBe("");
    }
  });
});
