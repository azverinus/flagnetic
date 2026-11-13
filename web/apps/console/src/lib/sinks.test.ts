import { i18n } from "@lingui/core";
import { describe, expect, it } from "vitest";

import { clickhouseSink, objectStorageSink, webhookSink } from "@/test/sinks";

import {
  bodyOf,
  attentionReasons,
  changedFieldNames,
  emptyForm,
  failureText,
  formOf,
  healthName,
  isUnchanged,
  lastDelivery,
  latestFailure,
  needsAttention,
  patchOf,
  pointerOfName,
  replayBodyOf,
  startFromOf,
  testBodyOf,
  validateSinkSearch,
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

describe("a failure of a target", () => {
  it("names the class, then the status and the code when the target gave them", () => {
    expect(failureText(i18n, { class: "connect", status: null, code: null })).toBe(
      "The connection failed",
    );
    expect(failureText(i18n, { class: "status", status: 502, code: null })).toBe(
      "The target answered with an error status (status 502)",
    );
    expect(failureText(i18n, { class: "rejected", status: 400, code: "UNKNOWN_TABLE" })).toBe(
      "The target rejected the batch (status 400, code UNKNOWN_TABLE)",
    );
    expect(failureText(i18n, { class: "status", status: null, code: "AccessDenied" })).toBe(
      "The target answered with an error status (code AccessDenied)",
    );
  });

  it("says something for a class this console does not know yet, never the target's words", () => {
    expect(failureText(i18n, { class: "newer", status: null, code: null })).toBe("newer");
    expect(failureText(i18n, { class: null, status: null, code: null })).toBe(
      "The target did not answer as needed",
    );
  });
});

describe("testing a saved sink", () => {
  it("tests what is stored while the connection is as stored", () => {
    const form = formOf(clickhouseSink);
    form.name = "Renamed";
    form.batch_size = "10";
    expect(testBodyOf(clickhouseSink, form)).toBeUndefined();
  });

  it("tests the form's configuration once it differs, a blank secret taking the stored one", () => {
    const form = formOf(webhookSink);
    form.webhook.url = "https://other.example.com/in";
    expect(testBodyOf(webhookSink, form)).toEqual({
      webhook: { url: "https://other.example.com/in", headers: { Authorization: "Bearer t" } },
    });
    const typed = formOf(clickhouseSink);
    typed.clickhouse.password = "new";
    expect(testBodyOf(clickhouseSink, typed)?.clickhouse?.password).toBe("new");
    const kept = formOf(clickhouseSink);
    kept.clickhouse.database = "other";
    expect(testBodyOf(clickhouseSink, kept)?.clickhouse).not.toHaveProperty("password");
  });
});

describe("a time to start from", () => {
  it("is the instant a local time names, or none while empty", () => {
    expect(startFromOf("")).toBeUndefined();
    expect(startFromOf("not a date")).toBeUndefined();
    expect(startFromOf("2026-11-01T10:30")).toBe(new Date("2026-11-01T10:30").toISOString());
  });
});

describe("a sink that needs attention", () => {
  const stopped = { ...webhookSink, health: "stopped" as const };

  it("is one that is stopped, at risk or holds an undismissed gap, and no other", () => {
    expect(needsAttention(webhookSink)).toBe(false);
    expect(needsAttention({ ...webhookSink, health: "failing", dead_letters: 2 })).toBe(false);
    expect(needsAttention(stopped)).toBe(true);
    expect(needsAttention({ ...webhookSink, at_risk: true })).toBe(true);
    expect(needsAttention({ ...webhookSink, gaps: 1 })).toBe(true);
  });

  it("says why, in the order of urgency", () => {
    expect(attentionReasons(i18n, { ...stopped, at_risk: true, gaps: 2 })).toEqual([
      "stopped",
      "at risk of losing events",
      "2 undismissed gaps",
    ]);
    expect(attentionReasons(i18n, { ...webhookSink, gaps: 1 })).toEqual(["1 undismissed gap"]);
  });

  it("names the latest failure across its streams, with the kind it happened on", () => {
    const at = (time: string) => ({
      class: "rejected" as const,
      status: 422,
      code: null,
      at: time,
    });
    const sink = {
      ...stopped,
      streams: [
        {
          event_kind: "exposure" as const,
          last_delivered_at: null,
          last_failure: at("2026-11-01T10:00:00Z"),
        },
        {
          event_kind: "alias" as const,
          last_delivered_at: null,
          last_failure: at("2026-11-01T11:00:00Z"),
        },
        { event_kind: "evaluation_summary" as const, last_delivered_at: null, last_failure: null },
      ],
    };
    expect(latestFailure(sink)?.eventKind).toBe("alias");
    expect(latestFailure(webhookSink)).toBeUndefined();
  });
});

describe("a replay", () => {
  it("sends the times as instants and every kind when none is ticked", () => {
    const result = replayBodyOf(i18n, {
      from: "2026-11-01T10:00",
      to: "",
      event_kinds: [],
    });
    expect(result).toEqual({ body: { from: new Date("2026-11-01T10:00").toISOString() } });
  });

  it("sends an end and the kinds ticked", () => {
    const result = replayBodyOf(i18n, {
      from: "2026-11-01T10:00",
      to: "2026-11-02T10:00",
      event_kinds: ["exposure"],
    });
    expect(result).toEqual({
      body: {
        from: new Date("2026-11-01T10:00").toISOString(),
        to: new Date("2026-11-02T10:00").toISOString(),
        event_kinds: ["exposure"],
      },
    });
  });

  it("is refused without a start, or with an end before it", () => {
    expect(replayBodyOf(i18n, { from: "", to: "", event_kinds: [] })).toEqual({
      problems: [{ path: "from", message: "Choose a time." }],
    });
    expect(
      replayBodyOf(i18n, { from: "2026-11-02T10:00", to: "2026-11-01T10:00", event_kinds: [] }),
    ).toEqual({ problems: [{ path: "to", message: "Choose a time after the start." }] });
  });
});

describe("a sink screen's search", () => {
  it("keeps a tab it knows and drops anything else", () => {
    expect(validateSinkSearch({ tab: "gaps" })).toEqual({ tab: "gaps" });
    expect(validateSinkSearch({ tab: "dead-letters" })).toEqual({ tab: "dead-letters" });
    expect(validateSinkSearch({ tab: "elsewhere" })).toEqual({});
  });
});
