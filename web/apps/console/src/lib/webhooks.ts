import {
  createInstanceWebhook,
  createWebhook,
  deleteInstanceWebhook,
  deleteWebhook,
  enableInstanceWebhook,
  enableWebhook,
  getInstanceWebhook,
  getInstanceWebhookDelivery,
  getWebhook,
  getWebhookDelivery,
  listInstanceWebhookDeliveries,
  listInstanceWebhooks,
  listWebhookDeliveries,
  listWebhookEventTypes,
  listWebhooks,
  redeliverInstanceWebhookDelivery,
  redeliverWebhookDelivery,
  rotateInstanceWebhookSecret,
  rotateWebhookSecret,
  testInstanceWebhook,
  testWebhook,
  updateInstanceWebhook,
  updateWebhook,
  type Webhook,
  type WebhookCreate,
  type WebhookDelivery,
  type WebhookDeliveryStatus,
  type WebhookEventTypeInfo,
  type WebhookPatch,
  zWebhookDeliveryStatus,
} from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";
import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";

import { unwrap } from "./errors";
import { ifMatch } from "./http";

/**
 * Where a webhook subscription lives: in one project, or at the instance,
 * where an instance admin subscribes to every audit entry. The two answer
 * the same operations under different paths, so the screens take a scope
 * and never ask which one they are in.
 */
export type WebhookScope = { project: string } | "instance";

/** The query keys of a scope's subscriptions, so a change invalidates by prefix. */
const keys = {
  all: (scope: WebhookScope) =>
    scope === "instance"
      ? (["instance", "webhooks"] as const)
      : (["projects", scope.project, "webhooks"] as const),
  one: (scope: WebhookScope, webhook: string) => [...keys.all(scope), webhook] as const,
  deliveries: (scope: WebhookScope, webhook: string) =>
    [...keys.one(scope, webhook), "deliveries"] as const,
};

/** Every query a scope's subscriptions are cached under. */
export const webhooksKey = keys.all;

/** Every query of one subscription: the subscription itself and its delivery log. */
export const webhookKey = keys.one;

/** Every read of one subscription's delivery log. */
export const deliveriesKey = keys.deliveries;

/** The operations of one scope, through the generated client. */
export function webhookApi(scope: WebhookScope) {
  const project = scope === "instance" ? undefined : scope.project;
  return {
    list: () => unwrap(project ? listWebhooks({ path: { project } }) : listInstanceWebhooks()),
    get: (webhook: string) =>
      unwrap(
        project
          ? getWebhook({ path: { project, webhook } })
          : getInstanceWebhook({ path: { webhook } }),
      ),
    create: (body: WebhookCreate) =>
      unwrap(
        project ? createWebhook({ path: { project }, body }) : createInstanceWebhook({ body }),
      ),
    update: (webhook: Webhook, body: WebhookPatch) =>
      unwrap(
        project
          ? updateWebhook({
              path: { project, webhook: webhook.id },
              body,
              headers: ifMatch(webhook.version),
            })
          : updateInstanceWebhook({
              path: { webhook: webhook.id },
              body,
              headers: ifMatch(webhook.version),
            }),
      ),
    remove: (webhook: string) =>
      unwrap(
        project
          ? deleteWebhook({ path: { project, webhook } })
          : deleteInstanceWebhook({ path: { webhook } }),
      ),
    enable: (webhook: string) =>
      unwrap(
        project
          ? enableWebhook({ path: { project, webhook } })
          : enableInstanceWebhook({ path: { webhook } }),
      ),
    rotateSecret: (webhook: string) =>
      unwrap(
        project
          ? rotateWebhookSecret({ path: { project, webhook } })
          : rotateInstanceWebhookSecret({ path: { webhook } }),
      ),
    test: (webhook: string) =>
      unwrap(
        project
          ? testWebhook({ path: { project, webhook } })
          : testInstanceWebhook({ path: { webhook } }),
      ),
    deliveries: (webhook: string, query: DeliveryQuery) =>
      unwrap(
        project
          ? listWebhookDeliveries({ path: { project, webhook }, query })
          : listInstanceWebhookDeliveries({ path: { webhook }, query }),
      ),
    delivery: (webhook: string, delivery: string) =>
      unwrap(
        project
          ? getWebhookDelivery({ path: { project, webhook, delivery } })
          : getInstanceWebhookDelivery({ path: { webhook, delivery } }),
      ),
    redeliver: (webhook: string, delivery: string) =>
      unwrap(
        project
          ? redeliverWebhookDelivery({ path: { project, webhook, delivery } })
          : redeliverInstanceWebhookDelivery({ path: { webhook, delivery } }),
      ),
  };
}

/** A scope's subscriptions, the oldest first. */
export function webhooksQuery(scope: WebhookScope) {
  return queryOptions({
    queryKey: keys.all(scope),
    queryFn: async () => (await webhookApi(scope).list()).items,
  });
}

export function webhookQuery(scope: WebhookScope, webhook: string) {
  return queryOptions({
    queryKey: keys.one(scope, webhook),
    queryFn: () => webhookApi(scope).get(webhook),
    retry: false,
  });
}

/** The event catalogue with what emits each type; it changes only with a release. */
export const webhookEventTypesQuery = queryOptions({
  queryKey: ["webhook-event-types"],
  queryFn: async () => (await unwrap(listWebhookEventTypes())).items,
  staleTime: Infinity,
});

/** What the delivery log is narrowed to, as the address carries it. */
export type DeliveryFilters = {
  status?: WebhookDeliveryStatus;
  event_type?: string;
  /** A whole day (YYYY-MM-DD) in the member's time zone. */
  from?: string;
  to?: string;
};

type DeliveryQuery = {
  status?: WebhookDeliveryStatus;
  event_type?: string;
  from?: string;
  to?: string;
  cursor?: string;
  limit?: number;
};

const day = /^\d{4}-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])$/;

/** One subscription's screen as search parameters: the open tab, a delivery and the log's filters. */
export type WebhookSearch = DeliveryFilters & {
  tab?: "deliveries";
  /** The delivery whose attempts are open beside the log. */
  delivery?: string;
};

/** Reads a subscription screen's search parameters, dropping anything it cannot use. */
export function validateWebhookSearch(search: Record<string, unknown>): WebhookSearch {
  const out: WebhookSearch = {};
  if (search.tab === "deliveries") out.tab = "deliveries";
  const status = zWebhookDeliveryStatus.safeParse(search.status);
  if (status.success) out.status = status.data;
  for (const name of ["event_type", "delivery"] as const) {
    const value = search[name];
    if (typeof value === "string" && value.trim() !== "") out[name] = value.trim();
  }
  for (const name of ["from", "to"] as const) {
    const value = search[name];
    if (typeof value === "string" && day.test(value)) out[name] = value;
  }
  return out;
}

/** The first instant of a day in the member's time zone, or its last. */
function instantOf(value: string, end: boolean): string {
  const [year, month, date] = value.split("-").map(Number) as [number, number, number];
  return (
    end
      ? new Date(year, month - 1, date, 23, 59, 59, 999)
      : new Date(year, month - 1, date, 0, 0, 0, 0)
  ).toISOString();
}

/** What the listing operations are asked for a filter. */
export function deliveryQueryOf(filters: DeliveryFilters): DeliveryQuery {
  const { from, to, ...rest } = filters;
  return {
    ...rest,
    ...(from ? { from: instantOf(from, false) } : {}),
    ...(to ? { to: instantOf(to, true) } : {}),
  };
}

/** Whether any filter of the delivery log is set. */
export function isDeliveryFiltered(filters: DeliveryFilters): boolean {
  return Object.values(filters).some((value) => value !== undefined);
}

/** How soon the log asks again while a delivery is pending, and otherwise. */
const pendingRefresh = 1_500;
const idleRefresh = 5_000;

/**
 * One subscription's delivery log, the newest first, a page at a time. The
 * log asks again by itself, soon while a delivery is still pending, so a
 * test or a retry settles in front of the member and an event that arrives
 * shows without a reload.
 */
export function deliveriesQuery(scope: WebhookScope, webhook: string, filters: DeliveryFilters) {
  const query = deliveryQueryOf(filters);
  return infiniteQueryOptions({
    queryKey: [...keys.deliveries(scope, webhook), query] as const,
    queryFn: ({ pageParam }) =>
      webhookApi(scope).deliveries(webhook, pageParam ? { ...query, cursor: pageParam } : query),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    refetchInterval: (state) =>
      state.state.data?.pages.some((page) => page.items.some((d) => d.status === "pending"))
        ? pendingRefresh
        : idleRefresh,
  });
}

/** One delivery by its identifier, for an address that names one the log does not list. */
export function deliveryQuery(scope: WebhookScope, webhook: string, delivery: string) {
  return queryOptions({
    queryKey: [...keys.deliveries(scope, webhook), "one", delivery] as const,
    queryFn: () => webhookApi(scope).delivery(webhook, delivery),
    retry: false,
  });
}

/** Whether the platform disabled the subscription for failing, which only enabling it again undoes. */
export function isPlatformDisabled(webhook: Webhook): boolean {
  return !webhook.enabled && webhook.disabled_reason === "persistent_failure";
}

/** The types a subscription names individually: not "every type", not the ping a test sends. */
const ungrouped: readonly string[] = ["*", "audit.entry", "ping"];

/** The type that stands for every event type but the audit entries. */
export const everyType = "*";

/** The type of an instance subscription, and the one a project subscription asks for apart. */
export const auditEntryType = "audit.entry";

export type EventGroup = { entity: string; types: WebhookEventTypeInfo[] };

/**
 * The catalogue's event types grouped by the entity each is about, in the
 * catalogue's own order. `*` and `audit.entry` are chosen apart, and
 * `ping` is what a test sends whatever the subscription names.
 */
export function eventGroups(catalogue: readonly WebhookEventTypeInfo[]): EventGroup[] {
  const groups = new Map<string, EventGroup>();
  for (const info of catalogue) {
    if (ungrouped.includes(info.type)) continue;
    const entity = info.type.slice(0, info.type.indexOf("."));
    const group = groups.get(entity) ?? { entity, types: [] };
    group.types.push(info);
    groups.set(entity, group);
  }
  return [...groups.values()];
}

/** The name of the entity a group of event types is about. */
export function eventGroupName(i18n: I18n, entity: string): string {
  switch (entity) {
    case "flag":
      return i18n._(msg`Flags`);
    case "segment":
      return i18n._(msg`Segments`);
    case "experiment":
      return i18n._(msg`Experiments`);
    case "change_request":
      return i18n._(msg`Change requests`);
    case "grant":
      return i18n._(msg`Grants`);
    case "sdk_key":
      return i18n._(msg`SDK keys`);
    case "webhook":
      return i18n._(msg`Webhooks`);
    case "report":
      return i18n._(msg`Reports`);
    default:
      return entity;
  }
}

/** What a subscription names, in a few words, for the list. */
export function describeEvents(i18n: I18n, events: readonly string[]): string {
  const every = events.includes(everyType);
  const audit = events.includes(auditEntryType);
  if (every && audit) return i18n._(msg`Every event and every audit entry`);
  if (every) return i18n._(msg`Every event`);
  if (audit && events.length === 1) return i18n._(msg`Every audit entry`);
  const count = events.length;
  return count === 1 ? i18n._(msg`1 event type`) : i18n._(msg`${count} event types`);
}

/**
 * The values of the webhook form. Naming `*` covers every individual
 * type, so those are dropped when the body is built.
 */
export type WebhookForm = {
  name: string;
  url: string;
  events: string[];
  /** The keys of the environments whose events are sent; none for every one. */
  environments: string[];
  enabled: boolean;
};

/** The form a subscription opens with. */
export function formOf(webhook: Webhook): WebhookForm {
  return {
    name: webhook.name,
    url: webhook.url,
    events: [...webhook.events],
    environments: [...webhook.environments],
    enabled: webhook.enabled,
  };
}

/**
 * The body of a write: `*` stands for every type but the audit entries, so
 * the individual types it covers are not sent beside it.
 */
export function bodyOf(values: WebhookForm): WebhookCreate {
  const events = values.events.includes(everyType)
    ? values.events.filter((type) => type === everyType || type === auditEntryType)
    : values.events;
  return {
    name: values.name.trim(),
    url: values.url.trim(),
    events: events as WebhookCreate["events"],
    environments: values.environments,
    enabled: values.enabled,
  };
}

/** The properties of a form that differ from the subscription it was filled from. */
export function patchOf(webhook: Webhook, values: WebhookForm): WebhookPatch {
  const body = bodyOf(values);
  const patch: WebhookPatch = {};
  if (body.name !== webhook.name) patch.name = body.name;
  if (body.url !== webhook.url) patch.url = body.url;
  if (!sameSet(body.events, webhook.events)) patch.events = body.events;
  if (!sameSet(body.environments ?? [], webhook.environments))
    patch.environments = body.environments;
  if (body.enabled !== undefined && body.enabled !== webhook.enabled) patch.enabled = body.enabled;
  return patch;
}

function sameSet(a: readonly string[], b: readonly string[]): boolean {
  return a.length === b.length && a.every((item) => b.includes(item));
}

/**
 * The editor field a refusal of a write belongs on: the target the
 * instance will not reach, or an event type the catalogue does not know.
 */
export function fieldOfRefusal(code: string): "url" | "events" | undefined {
  if (code === "insecure_url" || code === "private_network_target") return "url";
  if (code === "unknown_event_type") return "events";
  return undefined;
}

export type Tone = "success" | "failure" | "pending" | "muted";

/** The name of a delivery's status and the tone it is shown in. */
export function deliveryStatus(
  i18n: I18n,
  status: WebhookDeliveryStatus,
): { label: string; tone: Tone } {
  switch (status) {
    case "pending":
      return { label: i18n._(msg`Pending`), tone: "pending" };
    case "succeeded":
      return { label: i18n._(msg`Succeeded`), tone: "success" };
    case "failed":
      return { label: i18n._(msg`Failed`), tone: "failure" };
    case "exhausted":
      return { label: i18n._(msg`Exhausted`), tone: "failure" };
    case "dropped":
      return { label: i18n._(msg`Dropped`), tone: "muted" };
  }
}

export const deliveryStatuses: readonly WebhookDeliveryStatus[] = zWebhookDeliveryStatus.options;

/** What a delivery's latest attempt answered: the status code, or that none came. */
export function latestResponse(i18n: I18n, delivery: WebhookDelivery): string {
  const latest = delivery.attempts.at(-1);
  if (!latest) return "—";
  return latest.status_code === null ? i18n._(msg`No response`) : String(latest.status_code);
}
