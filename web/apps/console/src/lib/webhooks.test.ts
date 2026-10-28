import type { Webhook, WebhookEventTypeInfo } from "@flagnetic/api";
import { i18n } from "@lingui/core";
import { describe, expect, it } from "vitest";

import {
  bodyOf,
  deliveryQueryOf,
  describeEvents,
  eventGroups,
  fieldOfRefusal,
  formOf,
  isDeliveryFiltered,
  isPlatformDisabled,
  patchOf,
  validateWebhookSearch,
} from "./webhooks";

const webhook: Webhook = {
  id: "0192a000-0000-7000-8000-000000000001",
  name: "Release bot",
  url: "https://hooks.example.com/flagnetic",
  events: ["flag.kill_switch"],
  environments: ["production"],
  enabled: true,
  disabled_reason: null,
  created_by: null,
  last_success_at: null,
  consecutive_failures: 0,
  previous_secret_expires_at: null,
  created_at: "2026-10-25T10:00:00Z",
  updated_at: "2026-10-25T10:00:00Z",
  version: 1,
};

const catalogue: WebhookEventTypeInfo[] = [
  { type: "flag.created", description: "A flag was created." },
  { type: "flag.kill_switch", description: "The kill switch was used." },
  { type: "segment.created", description: "A segment was created." },
  { type: "webhook.disabled", description: "A subscription was disabled." },
  { type: "audit.entry", description: "Every audit entry." },
  { type: "ping", description: "A test." },
];

describe("the event picker's groups", () => {
  it("group the catalogue by entity and leave the types chosen apart out", () => {
    expect(eventGroups(catalogue).map((group) => [group.entity, group.types.length])).toEqual([
      ["flag", 2],
      ["segment", 1],
      ["webhook", 1],
    ]);
  });
});

describe("a subscription's body", () => {
  it("drops the types the every-event choice already covers, and keeps the audit entries", () => {
    const form = {
      ...formOf(webhook),
      events: ["flag.created", "*", "audit.entry", "segment.created"],
    };
    expect(bodyOf(form).events).toEqual(["*", "audit.entry"]);
  });

  it("trims the name and the URL", () => {
    const body = bodyOf({ ...formOf(webhook), name: "  Bot ", url: " https://a.example.com " });
    expect([body.name, body.url]).toEqual(["Bot", "https://a.example.com"]);
  });

  it("send only what differs from the subscription an edit was filled from", () => {
    expect(patchOf(webhook, formOf(webhook))).toEqual({});
    expect(patchOf(webhook, { ...formOf(webhook), name: "Deploy bot", enabled: false })).toEqual({
      name: "Deploy bot",
      enabled: false,
    });
    // The order of the types and environments is no change.
    const reordered = { ...webhook, events: ["flag.created", "flag.kill_switch"] as never[] };
    expect(
      patchOf(reordered, { ...formOf(reordered), events: ["flag.kill_switch", "flag.created"] }),
    ).toEqual({});
  });
});

describe("what a subscription is sent", () => {
  it.each([
    [["*"], "Every event"],
    [["*", "audit.entry"], "Every event and every audit entry"],
    [["audit.entry"], "Every audit entry"],
    [["flag.created"], "1 event type"],
    [["flag.created", "segment.created"], "2 event types"],
  ])("%j reads %s", (events, expected) => {
    expect(describeEvents(i18n, events)).toBe(expected);
  });
});

describe("the platform's disable", () => {
  it("tells a subscription it disabled from one a member disabled", () => {
    expect(isPlatformDisabled(webhook)).toBe(false);
    expect(isPlatformDisabled({ ...webhook, enabled: false })).toBe(false);
    expect(
      isPlatformDisabled({ ...webhook, enabled: false, disabled_reason: "persistent_failure" }),
    ).toBe(true);
  });
});

describe("a refusal's field", () => {
  it.each([
    ["insecure_url", "url"],
    ["private_network_target", "url"],
    ["unknown_event_type", "events"],
    ["invalid_webhook", undefined],
  ])("%s belongs on %s", (code, field) => {
    expect(fieldOfRefusal(code)).toBe(field);
  });
});

describe("the delivery log's address", () => {
  it("keeps what the screen can use and drops the rest", () => {
    expect(
      validateWebhookSearch({
        tab: "deliveries",
        status: "failed",
        event_type: " ping ",
        from: "2026-10-01",
        to: "yesterday",
        delivery: "",
        other: 1,
      }),
    ).toEqual({ tab: "deliveries", status: "failed", event_type: "ping", from: "2026-10-01" });
    expect(validateWebhookSearch({ tab: "nonsense", status: "bogus" })).toEqual({});
  });

  it("asks the API for whole days in the member's time zone", () => {
    const query = deliveryQueryOf({ status: "failed", from: "2026-10-01", to: "2026-10-02" });
    expect(query.status).toBe("failed");
    expect(query.from).toBe(new Date(2026, 9, 1, 0, 0, 0, 0).toISOString());
    expect(query.to).toBe(new Date(2026, 9, 2, 23, 59, 59, 999).toISOString());
  });

  it("counts any filter, and nothing else, as filtering", () => {
    expect(isDeliveryFiltered({})).toBe(false);
    expect(isDeliveryFiltered({ event_type: "ping" })).toBe(true);
  });
});
