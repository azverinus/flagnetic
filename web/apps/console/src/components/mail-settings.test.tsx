import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { renderWithRouter } from "@/test/render";

import { MailSettingsScreen } from "./mail-settings";

const recipient = "ada@flagnetic.test";

const stored = {
  host: "smtp.example.com",
  port: 587,
  security: "starttls",
  username: "relay",
  password_set: true,
  from: "Flagnetic <flags@example.com>",
  updated_at: "2026-11-01T10:00:00Z",
};

const render = () => renderWithRouter(() => <MailSettingsScreen recipient={recipient} />);

async function retype(label: string, value: string) {
  const user = userEvent.setup();
  const field = screen.getByLabelText(label);
  await user.clear(field);
  if (value !== "") await user.type(field, value);
  return user;
}

describe("the mail settings", () => {
  it("says nothing is mailed while no SMTP server is configured, and offers no test", async () => {
    fakeServer({ "GET /instance/smtp": problem(404, "not_found") });
    await render();

    expect(await screen.findByText(/No SMTP server is configured/)).toBeVisible();
    expect(screen.getByLabelText("Port")).toHaveValue("587");
    expect(screen.queryByRole("button", { name: "Send a test mail" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Remove" })).not.toBeInTheDocument();
  });

  it("configures a server and then offers the test and the removal", async () => {
    const saved = { ...stored, username: "", password_set: false, from: "flags@example.com" };
    const server = fakeServer({
      "GET /instance/smtp": problem(404, "not_found"),
      "PUT /instance/smtp": { status: 200, body: saved },
    });
    await render();
    await screen.findByText(/No SMTP server is configured/);

    await retype("Host", "smtp.example.com");
    const user = await retype("Sender", "flags@example.com");
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(server.requests.some((r) => r.method === "PUT")).toBe(true));
    const put = server.requests.find((r) => r.method === "PUT")!;
    expect(await put.json()).toEqual({
      host: "smtp.example.com",
      port: 587,
      security: "starttls",
      username: "",
      from: "flags@example.com",
    });
    expect(await screen.findByRole("button", { name: "Send a test mail" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Remove" })).toBeVisible();
    expect(screen.queryByText(/No SMTP server is configured/)).not.toBeInTheDocument();
  });

  it("never shows the stored password and keeps it when left empty", async () => {
    const server = fakeServer({
      "GET /instance/smtp": { status: 200, body: stored },
      "PUT /instance/smtp": {
        status: 200,
        body: { ...stored, from: "Flags <noreply@example.com>" },
      },
    });
    await render();

    expect(await screen.findByLabelText("Password")).toHaveValue("");
    expect(screen.getByText(/A password is stored and never shown again/)).toBeVisible();
    const user = await retype("Sender", "Flags <noreply@example.com>");
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(server.requests.some((r) => r.method === "PUT")).toBe(true));
    const body = await server.requests.find((r) => r.method === "PUT")!.json();
    expect(body).not.toHaveProperty("password");
    expect(body).toMatchObject({ from: "Flags <noreply@example.com>", username: "relay" });
  });

  it("asks for the stored password again when the port moves, and sends nothing before", async () => {
    const server = fakeServer({ "GET /instance/smtp": { status: 200, body: stored } });
    await render();
    await screen.findByLabelText("Password");

    const user = await retype("Port", "2525");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("A username needs a password.")).toBeVisible();
    expect(server.requests.some((r) => r.method === "PUT")).toBe(false);
  });

  it("starts the form over from the answer of a save that changed nothing", async () => {
    // The server trims the host, finds nothing changed and answers the
    // stored settings with the same time of change.
    fakeServer({
      "GET /instance/smtp": { status: 200, body: stored },
      "PUT /instance/smtp": { status: 200, body: stored },
    });
    await render();
    await screen.findByLabelText("Host");

    const user = await retype("Host", "smtp.example.com ");
    expect(screen.getByRole("region", { name: "Unsaved changes" })).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(screen.queryByRole("region", { name: "Unsaved changes" })).not.toBeInTheDocument(),
    );
    expect(screen.getByLabelText("Host")).toHaveValue("smtp.example.com");
  });

  it("keeps the edits when the stored settings are read again", async () => {
    let current = stored;
    fakeServer({ "GET /instance/smtp": () => ({ status: 200, body: current }) });
    const { queryClient } = await render();
    await screen.findByLabelText("Host");

    await retype("Port", "2525");
    current = { ...stored, from: "flags@example.org", updated_at: "2026-11-02T10:00:00Z" };
    await queryClient.invalidateQueries({ queryKey: ["instance", "smtp"] });

    await waitFor(() => expect(screen.getByLabelText("Sender")).toHaveValue("flags@example.org"));
    expect(screen.getByLabelText("Port")).toHaveValue("2525");
    const bar = screen.getByRole("region", { name: "Unsaved changes" });

    await userEvent.click(within(bar).getByRole("button", { name: "Discard" }));
    expect(screen.getByLabelText("Port")).toHaveValue("587");
    expect(screen.getByLabelText("Sender")).toHaveValue("flags@example.org");
    expect(screen.queryByRole("region", { name: "Unsaved changes" })).not.toBeInTheDocument();
  });

  it("shows the server as removed when the test finds none configured", async () => {
    let current: typeof stored | null = stored;
    fakeServer({
      "GET /instance/smtp": () =>
        current ? { status: 200, body: current } : problem(404, "not_found"),
      "POST /instance/smtp/test": () => {
        current = null;
        return problem(409, "smtp_not_configured");
      },
    });
    await render();

    await userEvent.click(await screen.findByRole("button", { name: "Send a test mail" }));

    expect(await screen.findByText(/the instance mails nothing/)).toBeVisible();
    expect(screen.queryByRole("button", { name: "Send a test mail" })).not.toBeInTheDocument();
  });

  it("shows the server as removed when another admin removed it first", async () => {
    let current: typeof stored | null = stored;
    fakeServer({
      "GET /instance/smtp": () =>
        current ? { status: 200, body: current } : problem(404, "not_found"),
      "DELETE /instance/smtp": () => {
        current = null;
        return problem(404, "not_found");
      },
    });
    await render();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Remove" }));
    const dialog = screen.getByRole("dialog", { name: "Remove the SMTP server" });
    await user.click(within(dialog).getByRole("button", { name: "Remove SMTP server" }));

    expect(await screen.findByText(/the instance mails nothing/)).toBeVisible();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("says the server's refusal of a field under it", async () => {
    fakeServer({
      "GET /instance/smtp": { status: 200, body: stored },
      "PUT /instance/smtp": problem(422, "invalid_smtp_settings", {
        errors: [
          { pointer: "/from", code: "invalid_sender", detail: "" },
          { pointer: "/security", code: "insecure_credentials", detail: "" },
        ],
      }),
    });
    await render();
    await screen.findByLabelText("Host");

    const user = await retype("Sender", "Flagnetic");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText(/Enter one address, optionally with a name/)).toBeVisible();
    expect(screen.getByText(/only over STARTTLS or TLS/)).toBeVisible();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("explains that the public address must be set first", async () => {
    fakeServer({
      "GET /instance/smtp": problem(404, "not_found"),
      "PUT /instance/smtp": problem(422, "base_url_required"),
    });
    await render();
    await screen.findByText(/No SMTP server is configured/);

    await retype("Host", "smtp.example.com");
    const user = await retype("Sender", "flags@example.com");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      /FLAGNETIC_BASE_URL.*every link the instance mails/,
    );
  });

  it("sends a test mail to the member and says it was accepted", async () => {
    const server = fakeServer({
      "GET /instance/smtp": { status: 200, body: stored },
      "POST /instance/smtp/test": { status: 204 },
    });
    await render();

    await userEvent.click(await screen.findByRole("button", { name: "Send a test mail" }));

    expect(await screen.findByRole("status")).toHaveTextContent(
      `The SMTP server accepted a test mail to ${recipient}.`,
    );
    expect(server.requests.filter((r) => r.method === "POST")).toHaveLength(1);
  });

  it("explains the step at which the test mail failed", async () => {
    fakeServer({
      "GET /instance/smtp": { status: 200, body: stored },
      "POST /instance/smtp/test": problem(502, "smtp_failed", { failure: "authentication" }),
    });
    await render();

    await userEvent.click(await screen.findByRole("button", { name: "Send a test mail" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(/refused the username and password/);
  });

  it("removes the server after confirmation and starts the form over", async () => {
    const server = fakeServer({
      "GET /instance/smtp": { status: 200, body: stored },
      "DELETE /instance/smtp": { status: 204 },
    });
    await render();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Remove" }));
    const dialog = screen.getByRole("dialog", { name: "Remove the SMTP server" });
    expect(dialog).toHaveTextContent(/stops mailing/);
    await user.click(within(dialog).getByRole("button", { name: "Remove SMTP server" }));

    expect(await screen.findByText(/No SMTP server is configured/)).toBeVisible();
    expect(server.requests.filter((r) => r.method === "DELETE")).toHaveLength(1);
    expect(screen.getByLabelText("Host")).toHaveValue("");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});
