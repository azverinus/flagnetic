import type { Sink, SinkWithSecret } from "@flagnetic/api";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { renderWithRouter } from "@/test/render";
import { clickhouseSink, objectStorageSink, webhookSink } from "@/test/sinks";

import { SinkEditor } from "./sink-editor";

const sinks = "/projects/checkout/sinks";
const common = {
  "GET /projects/checkout/environments": {
    status: 200,
    body: {
      items: [
        { id: "e1", key: "production", name: "Production" },
        { id: "e2", key: "staging", name: "Staging" },
      ],
    },
  },
};

function renderEditor(
  props: { sink?: Sink; onSaved?: (saved: Sink | SinkWithSecret) => void } = {},
) {
  return renderWithRouter(() => (
    <SinkEditor project="checkout" sink={props.sink} onSaved={props.onSaved ?? (() => undefined)} />
  ));
}

async function sentBody(server: { requests: Request[] }, method: string) {
  await waitFor(() => expect(server.requests.some((r) => r.method === method)).toBe(true));
  return (await server.requests.find((r) => r.method === method)!.json()) as Record<
    string,
    unknown
  >;
}

describe("creating a sink", () => {
  it("offers each kind with its description and shows the chosen kind's fields", async () => {
    fakeServer(common);
    await renderEditor();
    const user = userEvent.setup();

    expect(screen.getByRole("radio", { name: /ClickHouse/ })).toBeChecked();
    expect(screen.getByRole("radio", { name: /Inserts events into tables/ })).toBeInTheDocument();
    expect(screen.getByLabelText("Password")).toBeInTheDocument();

    await user.click(screen.getByRole("radio", { name: /Object storage/ }));
    expect(screen.getByLabelText("Bucket")).toBeInTheDocument();
    expect(screen.queryByLabelText("Password")).toBeNull();

    await user.click(screen.getByRole("radio", { name: /^Webhook/ }));
    expect(screen.getByLabelText("URL")).toBeInTheDocument();
    expect(screen.queryByLabelText("Bucket")).toBeNull();
  });

  it("shows each kind's batching defaults as placeholders", async () => {
    fakeServer(common);
    await renderEditor();
    const user = userEvent.setup();
    expect(screen.getByLabelText("Batch size")).toHaveAttribute("placeholder", "5000");
    expect(screen.getByLabelText("Flush interval (seconds)")).toHaveAttribute("placeholder", "10");
    await user.click(screen.getByRole("radio", { name: /Object storage/ }));
    expect(screen.getByLabelText("Batch size")).toHaveAttribute("placeholder", "500000");
    expect(screen.getByLabelText("Flush interval (seconds)")).toHaveAttribute("placeholder", "300");
    await user.click(screen.getByRole("radio", { name: /^Webhook/ }));
    expect(screen.getByLabelText("Batch size")).toHaveAttribute("placeholder", "500");
    expect(screen.getByLabelText("Flush interval (seconds)")).toHaveAttribute("placeholder", "5");
  });

  it("asks for what a ClickHouse sink needs before sending anything", async () => {
    const server = fakeServer(common);
    await renderEditor();
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Create sink" }));
    expect(await screen.findByText("Enter a name.")).toBeInTheDocument();
    expect(screen.getAllByText("Required.")).toHaveLength(4);
    expect(server.requests.some((r) => r.method === "POST")).toBe(false);
  });

  it("creates a ClickHouse sink", async () => {
    const server = fakeServer({
      ...common,
      [`POST ${sinks}`]: { status: 201, body: clickhouseSink },
    });
    let saved: unknown;
    await renderEditor({ onSaved: (s) => (saved = s) });
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Name"), "Warehouse");
    await user.type(screen.getByLabelText("URL"), "https://ch.example.com:8443");
    await user.type(screen.getByLabelText("Database"), "analytics");
    await user.type(screen.getByLabelText("Username"), "writer");
    await user.type(screen.getByLabelText("Password"), "s3cret");
    await user.click(screen.getByRole("checkbox", { name: /Exposures/ }));
    await user.click(await screen.findByRole("checkbox", { name: "Staging" }));
    await user.click(screen.getByRole("button", { name: "Add a setting" }));
    await user.type(screen.getByLabelText("Setting name"), "max_threads");
    await user.type(screen.getByLabelText("Setting value"), "4");
    await user.click(screen.getByRole("button", { name: "Create sink" }));

    await waitFor(() => expect(saved).toBeDefined());
    expect(await sentBody(server, "POST")).toEqual({
      name: "Warehouse",
      kind: "clickhouse",
      clickhouse: {
        url: "https://ch.example.com:8443",
        database: "analytics",
        table_prefix: "",
        username: "writer",
        password: "s3cret",
        tls_skip_verify: false,
        create_tables: true,
        settings: { max_threads: "4" },
      },
      event_kinds: ["exposure"],
      environments: ["staging"],
    });
  });

  it("creates an object storage sink, and drops the compression Parquet does not take", async () => {
    const server = fakeServer({
      ...common,
      [`POST ${sinks}`]: { status: 201, body: objectStorageSink },
    });
    await renderEditor();
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Name"), "Lake");
    await user.click(screen.getByRole("radio", { name: /Object storage/ }));
    await user.type(screen.getByLabelText("Bucket"), "events");
    await user.type(screen.getByLabelText("Access key ID"), "AKIA");
    await user.type(screen.getByLabelText("Secret access key"), "key");
    expect(screen.getByLabelText("Compression")).toBeInTheDocument();
    await user.click(screen.getByLabelText("Format"));
    await user.click(await screen.findByRole("option", { name: "Parquet" }));
    expect(screen.queryByLabelText("Compression")).toBeNull();
    await user.type(screen.getByLabelText("Batch size"), "1000");
    await user.click(screen.getByRole("button", { name: "Create sink" }));

    expect(await sentBody(server, "POST")).toMatchObject({
      name: "Lake",
      kind: "object_storage",
      object_storage: {
        bucket: "events",
        access_key_id: "AKIA",
        secret_access_key: "key",
        format: "parquet",
        compression: "",
      },
      batch_size: 1000,
    });
  });

  it("creates a webhook sink and hands over the secret it was answered", async () => {
    const server = fakeServer({
      ...common,
      [`POST ${sinks}`]: { status: 201, body: { ...webhookSink, secret: "once" } },
    });
    let saved: (Sink & { secret?: string }) | undefined;
    await renderEditor({ onSaved: (s) => (saved = s) });
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Name"), "Pipeline");
    await user.click(screen.getByRole("radio", { name: /^Webhook/ }));
    await user.type(screen.getByLabelText("URL"), "https://pipeline.example.com/in");
    await user.click(screen.getByRole("button", { name: "Add a header" }));
    await user.type(screen.getByLabelText("Header name"), "Authorization");
    await user.type(screen.getByLabelText("Header value"), "Bearer t");
    await user.click(screen.getByRole("button", { name: "Create sink" }));

    expect(await sentBody(server, "POST")).toEqual({
      name: "Pipeline",
      kind: "webhook",
      webhook: {
        url: "https://pipeline.example.com/in",
        headers: { Authorization: "Bearer t" },
      },
      event_kinds: [],
      environments: [],
    });
    await waitFor(() => expect(saved?.secret).toBe("once"));
  });

  it("explains the server's refusals under their fields", async () => {
    fakeServer({
      ...common,
      [`POST ${sinks}`]: problem(422, "invalid_sink", {
        errors: [
          { pointer: "/webhook/url", code: "insecure_url", detail: "x" },
          { pointer: "/webhook/headers/X-Flagnetic-Id", code: "reserved", detail: "x" },
          {
            pointer: "/batch_size",
            code: "out_of_range",
            detail: "x",
            params: { min: 1, max: 5000 },
          },
        ],
      }),
    });
    await renderEditor();
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Name"), "Pipeline");
    await user.click(screen.getByRole("radio", { name: /^Webhook/ }));
    await user.type(screen.getByLabelText("URL"), "http://pipeline.example.com/in");
    await user.click(screen.getByRole("button", { name: "Add a header" }));
    await user.type(screen.getByLabelText("Header name"), "X-Flagnetic-Id");
    await user.click(screen.getByRole("button", { name: "Create sink" }));

    expect(await screen.findByText(/accepts only https targets/)).toBeInTheDocument();
    expect(
      screen.getByText("The X-Flagnetic- headers are the platform's own."),
    ).toBeInTheDocument();
    expect(screen.getByText("Enter a value from 1 to 5,000.")).toBeInTheDocument();
    // Every violation is under a field, so the form adds no general message.
    expect(screen.queryByText("Some fields need correcting.")).toBeNull();
  });
});

describe("editing a sink", () => {
  it("never shows a stored secret and keeps it when saving other changes", async () => {
    const saved = { ...clickhouseSink, name: "Analytics", version: 2 };
    const server = fakeServer({
      ...common,
      [`PATCH ${sinks}/${clickhouseSink.id}`]: { status: 200, body: saved },
    });
    await renderEditor({ sink: clickhouseSink });
    const user = userEvent.setup();

    const password = screen.getByLabelText("Password");
    expect(password).toHaveValue("");
    expect(password).toHaveAttribute("placeholder", "***");
    expect(screen.getByText(/Unchanged unless you type a new one/)).toBeInTheDocument();
    expect(screen.queryByRole("radio")).toBeNull();
    expect(screen.getByLabelText("URL")).toHaveValue("https://ch.example.com:8443");

    await user.clear(screen.getByLabelText("Name"));
    await user.type(screen.getByLabelText("Name"), "Analytics");
    expect(screen.getByRole("region", { name: "Unsaved changes" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save" }));

    const request = server.requests.find((r) => r.method === "PATCH")!;
    await waitFor(() => expect(request).toBeDefined());
    expect(request.headers.get("If-Match")).toBe('"1"');
    expect(await request.json()).toEqual({ name: "Analytics" });
  });

  it("sends a typed secret with the rest of the configuration", async () => {
    const server = fakeServer({
      ...common,
      [`PATCH ${sinks}/${objectStorageSink.id}`]: { status: 200, body: objectStorageSink },
    });
    await renderEditor({ sink: objectStorageSink });
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Secret access key"), "rotated");
    await user.click(screen.getByRole("button", { name: "Save" }));
    const body = await sentBody(server, "PATCH");
    expect(body).toMatchObject({
      object_storage: { bucket: "events", access_key_id: "AKIA", secret_access_key: "rotated" },
    });
    expect(body).not.toHaveProperty("name");
  });

  it("fixes the kind and fills the webhook's headers", async () => {
    fakeServer(common);
    await renderEditor({ sink: webhookSink });
    expect(screen.getByText("A sink's kind is fixed when it is created.")).toBeInTheDocument();
    expect(screen.getByLabelText("Header name")).toHaveValue("Authorization");
    expect(screen.getByLabelText("Header value")).toHaveValue("Bearer t");
  });

  it("shows who saved first and keeps the member's edits on request", async () => {
    const newer = { ...clickhouseSink, name: "Renamed elsewhere", version: 2 };
    let calls = 0;
    const server = fakeServer({
      ...common,
      [`PATCH ${sinks}/${clickhouseSink.id}`]: () =>
        ++calls === 1
          ? problem(412, "version_mismatch")
          : {
              status: 200,
              body: {
                ...newer,
                clickhouse: { ...newer.clickhouse!, database: "events" },
                version: 3,
              },
            },
      [`GET ${sinks}/${clickhouseSink.id}`]: { status: 200, body: newer },
      "GET /projects/checkout/audit": { status: 200, body: { items: [] } },
    });
    await renderEditor({ sink: clickhouseSink });
    const user = userEvent.setup();
    await user.clear(screen.getByLabelText("Database"));
    await user.type(screen.getByLabelText("Database"), "events");
    await user.click(screen.getByRole("button", { name: "Save" }));

    const banner = await screen.findByRole("alert");
    expect(within(banner).getByText("Name")).toBeInTheDocument();
    await user.click(within(banner).getByRole("button", { name: "Keep my edits" }));

    await waitFor(() =>
      expect(server.requests.filter((r) => r.method === "PATCH")).toHaveLength(2),
    );
    const second = server.requests.filter((r) => r.method === "PATCH")[1]!;
    expect(second.headers.get("If-Match")).toBe('"2"');
    expect(await second.json()).toMatchObject({ clickhouse: { database: "events" } });
  });
});
