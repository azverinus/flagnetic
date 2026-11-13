import {
  zName,
  createSink,
  deleteSink,
  discardSinkDeadLetter,
  dismissSinkGap,
  enableSink,
  getSink,
  listSinkDeadLetters,
  listSinkGaps,
  listSinks,
  redeliverSinkDeadLetter,
  replaySink,
  resumeSink,
  rotateSinkSecret,
  sampleSink,
  type Sink,
  type SinkDeadLetterStatus,
  type SinkCreate,
  type SinkEventKind,
  type SinkFailureClass,
  type SinkHealth,
  type SinkKind,
  type SinkPatch,
  type SinkReplay,
  type SinkStream,
  type SinkTest,
  testSink,
  testSinkConfig,
  updateSink,
} from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg, plural } from "@lingui/core/macro";
import { queryOptions } from "@tanstack/react-query";

import { unwrap } from "./errors";
import { ifMatch } from "./http";

/** The query keys of a project's sinks, so a change invalidates by prefix. */
const keys = {
  all: (project: string) => ["projects", project, "sinks"] as const,
  one: (project: string, sink: string) => [...keys.all(project), sink] as const,
};

/** Every query a project's sinks are cached under. */
export const sinksKey = keys.all;

/** Every query of one sink. */
export const sinkKey = keys.one;

/** The operations on a project's sinks, through the generated client. */
export function sinkApi(project: string) {
  return {
    list: () => unwrap(listSinks({ path: { project } })),
    get: (sink: string) => unwrap(getSink({ path: { project, sink } })),
    create: (body: SinkCreate) => unwrap(createSink({ path: { project }, body })),
    /** `sink` names the version the write is made against, which may be newer than the form's. */
    update: (sink: { id: string; version: number }, body: SinkPatch) =>
      unwrap(
        updateSink({ path: { project, sink: sink.id }, body, headers: ifMatch(sink.version) }),
      ),
    remove: (sink: { id: string; version: number }) =>
      unwrap(deleteSink({ path: { project, sink: sink.id }, headers: ifMatch(sink.version) })),
    rotateSecret: (sink: string) => unwrap(rotateSinkSecret({ path: { project, sink } })),
    /** `startFrom` is a time within retention; absent, the streams start from now. */
    enable: (sink: string, startFrom?: string) =>
      unwrap(
        enableSink({
          path: { project, sink },
          body: startFrom === undefined ? {} : { start_from: startFrom },
        }),
      ),
    /** Stops delivery and keeps the cursor, so enabling it again continues. */
    disable: (sink: { id: string; version: number }) =>
      unwrap(
        updateSink({
          path: { project, sink: sink.id },
          body: { enabled: false },
          headers: ifMatch(sink.version),
        }),
      ),
    /** Tests a configuration nothing has stored yet. */
    testConfig: (body: SinkCreate) => unwrap(testSinkConfig({ path: { project }, body })),
    /** Tests the stored configuration, or the one in `body` against the stored credentials. */
    test: (sink: string, body?: SinkTest) =>
      unwrap(testSink({ path: { project, sink }, body: body ?? {} })),
    sample: (sink: string, eventKinds: SinkEventKind[]) =>
      unwrap(sampleSink({ path: { project, sink }, body: { event_kinds: eventKinds } })),
    /** Starts a stopped sink again where it was; a sink that is not stopped is answered as it is. */
    resume: (sink: string) => unwrap(resumeSink({ path: { project, sink } })),
    /** Sends the events received since `from` again; the sink applies it before its next batch. */
    replay: (sink: string, body: SinkReplay) =>
      unwrap(replaySink({ path: { project, sink }, body })),
    deadLetters: (sink: string) => unwrap(listSinkDeadLetters({ path: { project, sink } })),
    /** Sends a dead letter's stored rows to the target now; the answer is the dead letter after the attempt. */
    redeliver: (sink: string, deadLetter: string) =>
      unwrap(redeliverSinkDeadLetter({ path: { project, sink, deadLetter } })),
    discard: (sink: string, deadLetter: string) =>
      unwrap(discardSinkDeadLetter({ path: { project, sink, deadLetter } })),
    gaps: (sink: string) => unwrap(listSinkGaps({ path: { project, sink } })),
    dismissGap: (sink: string, gap: string) =>
      unwrap(dismissSinkGap({ path: { project, sink, gap } })),
  };
}

/** A project's sinks, the oldest first. */
export function sinksQuery(project: string) {
  return queryOptions({
    queryKey: keys.all(project),
    queryFn: async () => (await sinkApi(project).list()).items,
  });
}

export function sinkQuery(project: string, sink: string) {
  return queryOptions({
    queryKey: keys.one(project, sink),
    queryFn: () => sinkApi(project).get(sink),
    retry: false,
  });
}

/** A sink's dead letters, the newest first as the server lists them. */
export function deadLettersQuery(project: string, sink: string) {
  return queryOptions({
    queryKey: [...keys.one(project, sink), "dead-letters"] as const,
    queryFn: async () => (await sinkApi(project).deadLetters(sink)).items,
  });
}

/** A sink's gaps, dismissed ones included. */
export function gapsQuery(project: string, sink: string) {
  return queryOptions({
    queryKey: [...keys.one(project, sink), "gaps"] as const,
    queryFn: async () => (await sinkApi(project).gaps(sink)).items,
  });
}

/** The tabs of a sink's page beside its settings, which are the plain address. */
export type SinkSearch = { tab?: "dead-letters" | "replay" | "gaps" };

/** Reads a sink screen's search parameters, dropping anything it cannot use. */
export function validateSinkSearch(search: Record<string, unknown>): SinkSearch {
  const tab = search.tab;
  return tab === "dead-letters" || tab === "replay" || tab === "gaps" ? { tab } : {};
}

/** Whether a sink is stopped, at risk of losing events or has a gap nobody dismissed. */
export function needsAttention(sink: Sink): boolean {
  return sink.health === "stopped" || sink.at_risk || sink.gaps > 0;
}

/** Why a sink needs attention, each in a few words; none when it does not. */
export function attentionReasons(i18n: I18n, sink: Sink): string[] {
  const reasons: string[] = [];
  if (sink.health === "stopped") reasons.push(i18n._(msg`stopped`));
  if (sink.at_risk) reasons.push(i18n._(msg`at risk of losing events`));
  if (sink.gaps > 0) {
    reasons.push(
      i18n._(msg`${plural(sink.gaps, { one: "# undismissed gap", other: "# undismissed gaps" })}`),
    );
  }
  return reasons;
}

/** The last failure across a sink's streams, with the event kind it happened on. */
export function latestFailure(
  sink: Sink,
): { eventKind: SinkEventKind; failure: NonNullable<SinkStream["last_failure"]> } | undefined {
  let latest: ReturnType<typeof latestFailure>;
  for (const stream of sink.streams) {
    const failure = stream.last_failure;
    // Compared as instants: the server trims trailing zeros of the
    // fraction, so two times of one second differ in length as text.
    if (
      failure &&
      (latest === undefined || Date.parse(failure.at) > Date.parse(latest.failure.at))
    ) {
      latest = { eventKind: stream.event_kind, failure };
    }
  }
  return latest;
}

/** The name of a dead letter's status. */
export function deadLetterStatusName(i18n: I18n, status: SinkDeadLetterStatus): string {
  switch (status) {
    case "pending":
      return i18n._(msg`Pending`);
    case "redelivered":
      return i18n._(msg`Redelivered`);
    case "expired":
      return i18n._(msg`Expired`);
  }
}

/** What a replay form holds: times as a `datetime-local` input does, and the kinds ticked. */
export type ReplayForm = { from: string; to: string; event_kinds: SinkEventKind[] };

/** The body replaying a sink, or why the form cannot be sent yet. */
export function replayBodyOf(
  i18n: I18n,
  values: ReplayForm,
): { body: SinkReplay } | { problems: { path: string; message: string }[] } {
  const from = startFromOf(values.from);
  const to = startFromOf(values.to);
  const problems: { path: string; message: string }[] = [];
  if (from === undefined) problems.push({ path: "from", message: i18n._(msg`Choose a time.`) });
  if (values.to.trim() !== "" && to === undefined) {
    problems.push({ path: "to", message: i18n._(msg`Choose a time.`) });
  } else if (from !== undefined && to !== undefined && to < from) {
    problems.push({ path: "to", message: i18n._(msg`Choose a time after the start.`) });
  }
  if (problems.length > 0 || from === undefined) return { problems };
  return {
    body: {
      from,
      ...(to === undefined ? {} : { to }),
      ...(values.event_kinds.length === 0 ? {} : { event_kinds: values.event_kinds }),
    },
  };
}

export const sinkKinds: readonly SinkKind[] = ["clickhouse", "object_storage", "webhook"];

export const sinkEventKinds: readonly SinkEventKind[] = ["exposure", "alias", "evaluation_summary"];

/** The name of a kind of sink. */
export function kindName(i18n: I18n, kind: SinkKind): string {
  switch (kind) {
    case "clickhouse":
      return i18n._(msg`ClickHouse`);
    case "object_storage":
      return i18n._(msg`Object storage`);
    case "webhook":
      return i18n._(msg`Webhook`);
  }
}

/** What a kind of sink delivers to, in a sentence for the kind picker. */
export function kindDescription(i18n: I18n, kind: SinkKind): string {
  switch (kind) {
    case "clickhouse":
      return i18n._(
        msg`Inserts events into tables of your ClickHouse database over its HTTP interface, so analysts query them in place.`,
      );
    case "object_storage":
      return i18n._(
        msg`Writes a file per batch to an S3-compatible bucket, as JSON lines or Parquet, for a lake or a warehouse to load.`,
      );
    case "webhook":
      return i18n._(
        msg`Posts each batch to a URL of yours, signed with the sink's secret, for a pipeline you run.`,
      );
  }
}

/** The name of a kind of raw event a sink exports. */
export function eventKindName(i18n: I18n, kind: SinkEventKind): string {
  switch (kind) {
    case "exposure":
      return i18n._(msg`Exposures`);
    case "alias":
      return i18n._(msg`Aliases`);
    case "evaluation_summary":
      return i18n._(msg`Evaluation summaries`);
  }
}

/** What each kind of raw event is, for the picker. */
export function eventKindDescription(i18n: I18n, kind: SinkEventKind): string {
  switch (kind) {
    case "exposure":
      return i18n._(msg`A unit was shown a variation of an experiment.`);
    case "alias":
      return i18n._(msg`Two identifiers were declared to be the same unit.`);
    case "evaluation_summary":
      return i18n._(msg`How often SDKs and remote evaluation served each variation.`);
  }
}

/** What a kind batches by when the sink names no size or interval. */
export const batchingDefaults: Record<
  SinkKind,
  { batch_size: number; flush_interval: number; maxBatchSize?: number; flush?: [number, number] }
> = {
  clickhouse: { batch_size: 5_000, flush_interval: 10 },
  object_storage: { batch_size: 500_000, flush_interval: 300, flush: [60, 3_600] },
  webhook: { batch_size: 500, flush_interval: 5, maxBatchSize: 5_000 },
};

/** The name of a sink's health. */
export function healthName(i18n: I18n, health: SinkHealth): string {
  switch (health) {
    case "disabled":
      return i18n._(msg`Disabled`);
    case "stopped":
      return i18n._(msg`Stopped`);
    case "failing":
      return i18n._(msg`Failing`);
    case "degraded":
      return i18n._(msg`Degraded`);
    case "lagging":
      return i18n._(msg`Lagging`);
    case "healthy":
      return i18n._(msg`Healthy`);
  }
}

/** How loudly a health is shown. */
export function healthTone(health: SinkHealth): "default" | "muted" | "destructive" {
  switch (health) {
    case "healthy":
      return "default";
    case "disabled":
      return "muted";
    default:
      return "destructive";
  }
}

/** Why a call to a target failed, in a few words. */
export function failureClassName(i18n: I18n, failureClass: SinkFailureClass): string {
  switch (failureClass) {
    case "dns":
      return i18n._(msg`The address could not be resolved`);
    case "connect":
      return i18n._(msg`The connection failed`);
    case "tls":
      return i18n._(msg`The TLS handshake failed`);
    case "timeout":
      return i18n._(msg`The target did not answer in time`);
    case "refused_target":
      return i18n._(msg`The address is one this instance does not reach`);
    case "redirect":
      return i18n._(msg`The target answered with a redirect`);
    case "status":
      return i18n._(msg`The target answered with an error status`);
    case "rejected":
      return i18n._(msg`The target rejected the batch`);
  }
}

/**
 * A failure as the target's answer names it: the class in words, then the
 * HTTP status and the protocol's own code when there are any. Never the
 * target's own text, which the server does not send.
 */
export function failureText(
  i18n: I18n,
  failure: {
    class: SinkFailureClass | string | null;
    status?: number | null;
    code?: string | null;
  },
): string {
  const known = sinkFailureClasses.find((candidate) => candidate === failure.class);
  const head = known
    ? failureClassName(i18n, known)
    : failure.class
      ? failure.class
      : i18n._(msg`The target did not answer as needed`);
  const status = failure.status ?? undefined;
  const code = failure.code ?? undefined;
  if (status !== undefined && code) return i18n._(msg`${head} (status ${status}, code ${code})`);
  if (status !== undefined) return i18n._(msg`${head} (status ${status})`);
  if (code) return i18n._(msg`${head} (code ${code})`);
  return head;
}

const sinkFailureClasses: readonly SinkFailureClass[] = [
  "dns",
  "connect",
  "tls",
  "timeout",
  "refused_target",
  "redirect",
  "status",
  "rejected",
];

/** The sub-object a test of a saved sink sends, or none to test what is stored. */
export function testBodyOf(sink: Sink, values: SinkForm): SinkTest | undefined {
  const config = configOf(values);
  const secretTyped =
    (values.kind === "clickhouse" && values.clickhouse.password !== "") ||
    (values.kind === "object_storage" && values.object_storage.secret_access_key !== "");
  if (!secretTyped && JSON.stringify(config) === JSON.stringify(configOf(formOf(sink)))) {
    return undefined;
  }
  return config;
}

/** The moment a "from a time" input holds, as the server takes it, or none while it is empty or no date. */
export function startFromOf(local: string): string | undefined {
  if (local.trim() === "") return undefined;
  const at = new Date(local);
  return Number.isNaN(at.getTime()) ? undefined : at.toISOString();
}

/** When a sink last delivered any event kind, or none. */
export function lastDelivery(sink: Sink): string | null {
  let latest: string | null = null;
  for (const stream of sink.streams) {
    if (stream.last_delivered_at && (latest === null || stream.last_delivered_at > latest)) {
      latest = stream.last_delivered_at;
    }
  }
  return latest;
}

/** A header or setting as the form holds it: a name and a value. */
export type Pair = { name: string; value: string };

export type SinkForm = {
  name: string;
  kind: SinkKind;
  clickhouse: {
    url: string;
    database: string;
    table_prefix: string;
    username: string;
    password: string;
    tls_skip_verify: boolean;
    create_tables: boolean;
    settings: Pair[];
  };
  object_storage: {
    endpoint: string;
    region: string;
    bucket: string;
    prefix: string;
    access_key_id: string;
    secret_access_key: string;
    path_style: boolean;
    format: "jsonl" | "parquet";
    compression: "gzip" | "zstd" | "none";
  };
  webhook: { url: string; headers: Pair[] };
  /** The event kinds exported; none for every kind. */
  event_kinds: SinkEventKind[];
  /** The keys of the environments exported; none for every one. */
  environments: string[];
  /** Blank for the kind's default. */
  batch_size: string;
  flush_interval: string;
};

/** The form a new sink opens with. */
export function emptyForm(kind: SinkKind = "clickhouse"): SinkForm {
  return {
    name: "",
    kind,
    clickhouse: {
      url: "",
      database: "",
      table_prefix: "",
      username: "",
      password: "",
      tls_skip_verify: false,
      create_tables: true,
      settings: [],
    },
    object_storage: {
      endpoint: "",
      region: "",
      bucket: "",
      prefix: "",
      access_key_id: "",
      secret_access_key: "",
      path_style: false,
      format: "jsonl",
      compression: "gzip",
    },
    webhook: { url: "", headers: [] },
    event_kinds: [],
    environments: [],
    batch_size: "",
    flush_interval: "",
  };
}

const pairsOf = (record: Record<string, string> | undefined): Pair[] =>
  Object.entries(record ?? {}).map(([name, value]) => ({ name, value }));

/**
 * The form a stored sink opens with. A secret is write-only: the server
 * answers `***` for it, and the form starts it empty, which keeps it.
 */
export function formOf(sink: Sink): SinkForm {
  const form = emptyForm(sink.kind);
  form.name = sink.name;
  form.event_kinds = [...sink.event_kinds];
  form.environments = [...sink.environments];
  form.batch_size = String(sink.batch_size);
  form.flush_interval = String(sink.flush_interval);
  const { clickhouse, object_storage, webhook } = sink;
  if (clickhouse) {
    form.clickhouse = {
      url: clickhouse.url,
      database: clickhouse.database,
      table_prefix: clickhouse.table_prefix,
      username: clickhouse.username,
      password: "",
      tls_skip_verify: clickhouse.tls_skip_verify,
      create_tables: clickhouse.create_tables,
      settings: pairsOf(clickhouse.settings),
    };
  }
  if (object_storage) {
    form.object_storage = {
      endpoint: object_storage.endpoint,
      region: object_storage.region,
      bucket: object_storage.bucket,
      prefix: object_storage.prefix,
      access_key_id: object_storage.access_key_id,
      secret_access_key: "",
      path_style: object_storage.path_style,
      format: object_storage.format,
      compression: object_storage.compression,
    };
  }
  if (webhook) form.webhook = { url: webhook.url, headers: pairsOf(webhook.headers) };
  return form;
}

function recordOf(pairs: Pair[]): Record<string, string> {
  const record: Record<string, string> = {};
  for (const { name, value } of pairs) {
    if (name.trim() !== "") record[name.trim()] = value;
  }
  return record;
}

/** The configuration of the form's kind, as the server takes it; a blank secret is left out. */
function configOf(values: SinkForm): Pick<SinkCreate, "clickhouse" | "object_storage" | "webhook"> {
  switch (values.kind) {
    case "clickhouse": {
      const { password, settings, ...rest } = values.clickhouse;
      return {
        clickhouse: {
          ...rest,
          url: rest.url.trim(),
          database: rest.database.trim(),
          table_prefix: rest.table_prefix.trim(),
          username: rest.username.trim(),
          ...(password === "" ? {} : { password }),
          settings: recordOf(settings),
        },
      };
    }
    case "object_storage": {
      const { secret_access_key, ...rest } = values.object_storage;
      return {
        object_storage: {
          ...rest,
          endpoint: rest.endpoint.trim(),
          region: rest.region.trim(),
          bucket: rest.bucket.trim(),
          prefix: rest.prefix.trim(),
          access_key_id: rest.access_key_id.trim(),
          // Parquet files are always zstd: the choice does not apply.
          compression: rest.format === "parquet" ? "" : rest.compression,
          ...(secret_access_key === "" ? {} : { secret_access_key }),
        },
      };
    }
    case "webhook":
      return {
        webhook: { url: values.webhook.url.trim(), headers: recordOf(values.webhook.headers) },
      };
  }
}

function whole(value: string): number | undefined {
  const trimmed = value.trim();
  return trimmed === "" ? undefined : Number(trimmed);
}

/** The body creating a sink: batching left blank is the kind's default. */
export function bodyOf(values: SinkForm): SinkCreate {
  const batch_size = whole(values.batch_size);
  const flush_interval = whole(values.flush_interval);
  return {
    name: values.name.trim(),
    kind: values.kind,
    ...configOf(values),
    event_kinds: values.event_kinds,
    environments: values.environments,
    ...(batch_size === undefined ? {} : { batch_size }),
    ...(flush_interval === undefined ? {} : { flush_interval }),
  };
}

function sameSet(a: readonly string[], b: readonly string[]): boolean {
  return a.length === b.length && a.every((item) => b.includes(item));
}

/**
 * The properties of a form that differ from the sink it was filled from.
 * A kind's configuration is sent whole when any of it changed or a secret
 * was typed, since the server replaces it as a unit; a secret left empty
 * is left out and keeps the stored one. Batching cleared goes back to the
 * kind's default.
 */
export function patchOf(sink: Sink, values: SinkForm): SinkPatch {
  const before = formOf(sink);
  const body = bodyOf(values);
  const patch: SinkPatch = {};
  if (body.name !== sink.name) patch.name = body.name;
  if (!sameSet(values.event_kinds, before.event_kinds)) patch.event_kinds = values.event_kinds;
  if (!sameSet(values.environments, before.environments)) patch.environments = values.environments;
  const defaults = batchingDefaults[sink.kind];
  const batch = body.batch_size ?? defaults.batch_size;
  const flush = body.flush_interval ?? defaults.flush_interval;
  if (batch !== sink.batch_size) patch.batch_size = batch;
  if (flush !== sink.flush_interval) patch.flush_interval = flush;
  const config = configOf(values);
  const stored = configOf(before);
  const secretTyped =
    (values.kind === "clickhouse" && values.clickhouse.password !== "") ||
    (values.kind === "object_storage" && values.object_storage.secret_access_key !== "");
  if (secretTyped || JSON.stringify(config) !== JSON.stringify(stored)) {
    Object.assign(patch, config);
  }
  return patch;
}

/** Whether the form changes nothing about the sink. */
export function isUnchanged(sink: Sink, values: SinkForm): boolean {
  return Object.keys(patchOf(sink, values)).length === 0;
}

/** The JSON Pointer of a header or setting named in a request body. */
export function pointerOfName(prefix: string, name: string): string {
  return `${prefix}/${name.replaceAll("~", "~0").replaceAll("/", "~1")}`;
}

/** The names of the fields that differ between two versions of a sink, for the conflict banner. */
export function changedFieldNames(i18n: I18n, before: Sink, after: Sink): string[] {
  const names: string[] = [];
  const a = formOf(before);
  const b = formOf(after);
  if (a.name !== b.name) names.push(i18n._(msg`Name`));
  if (JSON.stringify(configOf(a)) !== JSON.stringify(configOf(b)))
    names.push(i18n._(msg`Connection`));
  if (!sameSet(a.event_kinds, b.event_kinds)) names.push(i18n._(msg`Event kinds`));
  if (!sameSet(a.environments, b.environments)) names.push(i18n._(msg`Environments`));
  if (a.batch_size !== b.batch_size || a.flush_interval !== b.flush_interval)
    names.push(i18n._(msg`Batching`));
  if (before.enabled !== after.enabled) names.push(i18n._(msg`Enabled`));
  return names;
}

/**
 * What the browser can tell is wrong with a form before asking the
 * server: a blank required field, a batching value that is no whole
 * number. Everything else, such as an address' shape, is the server's to
 * say, under its field. A secret is required only for a new sink.
 */
export function formProblems(
  i18n: I18n,
  values: SinkForm,
  editing: boolean,
): { path: string; message: string }[] {
  const out: { path: string; message: string }[] = [];
  const required = (path: string, value: string) => {
    if (value.trim() === "") out.push({ path, message: i18n._(msg`Required.`) });
  };
  if (values.name.trim() === "") out.push({ path: "name", message: i18n._(msg`Enter a name.`) });
  else if (!zName.safeParse(values.name.trim()).success) {
    out.push({ path: "name", message: i18n._(msg`Enter a name of 1 to 200 characters.`) });
  }
  switch (values.kind) {
    case "clickhouse":
      required("clickhouse.url", values.clickhouse.url);
      required("clickhouse.database", values.clickhouse.database);
      required("clickhouse.username", values.clickhouse.username);
      if (!editing) required("clickhouse.password", values.clickhouse.password);
      break;
    case "object_storage":
      required("object_storage.bucket", values.object_storage.bucket);
      required("object_storage.access_key_id", values.object_storage.access_key_id);
      if (!editing)
        required("object_storage.secret_access_key", values.object_storage.secret_access_key);
      break;
    case "webhook":
      required("webhook.url", values.webhook.url);
      break;
  }
  for (const name of ["batch_size", "flush_interval"] as const) {
    const value = values[name].trim();
    if (value !== "" && !/^[1-9]\d*$/.test(value)) {
      out.push({
        path: name,
        message: i18n._(msg`Enter a whole number of 1 or more, or leave it empty for the default.`),
      });
    }
  }
  return out;
}
