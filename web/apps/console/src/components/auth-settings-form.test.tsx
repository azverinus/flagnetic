import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { renderWithRouter } from "@/test/render";

import { AuthSettingsForm } from "./auth-settings-form";

const fresh = {
  session_idle_timeout_seconds: 86_400,
  session_absolute_timeout_seconds: 2_592_000,
  token_max_lifetime_seconds: 31_536_000,
  token_expiry_required: false,
  password_min_length: 12,
  self_service_password_reset: true,
  local_login_enabled: true,
  sso_enforced_domains: [],
};

const provider = (slug: string, name: string, domains: string[], enabled = true) => ({
  id: `id-${slug}`,
  slug,
  kind: "oidc",
  display_name: name,
  enabled,
  domains,
  link_by_email: false,
  auto_provision: false,
  version: 1,
});
const providers = {
  "GET /instance/auth-providers": {
    status: 200,
    body: {
      items: [
        provider("acme", "Acme SSO", ["acme.example", "shared.example"]),
        provider("globex", "Globex", ["shared.example"]),
        provider("dormant", "Dormant", ["dormant.example"], false),
      ],
    },
  },
};

/** The saves the form sent: it also reads the providers. */
const puts = (server: { requests: Request[] }) => server.requests.filter((r) => r.method === "PUT");

async function retype(label: string, value: string) {
  const user = userEvent.setup();
  const field = screen.getByLabelText(label);
  await user.clear(field);
  await user.type(field, value);
  return user;
}

describe("the auth settings form", () => {
  it("shows each duration in the unit that holds it whole", async () => {
    fakeServer({});
    await renderWithRouter(() => <AuthSettingsForm settings={fresh} />);

    expect(screen.getByLabelText("Session idle timeout")).toHaveValue("1");
    expect(
      screen.getByRole("combobox", { name: "Unit of the session idle timeout" }),
    ).toHaveTextContent("day");
    expect(screen.getByLabelText("Session absolute timeout")).toHaveValue("30");
    expect(
      screen.getByRole("combobox", { name: "Unit of the session absolute timeout" }),
    ).toHaveTextContent("days");
    expect(screen.getByLabelText("Password minimum length")).toHaveValue("12");
    expect(screen.queryByRole("region", { name: "Unsaved changes" })).not.toBeInTheDocument();
  });

  it("sends every setting in seconds, and is clean again once saved", async () => {
    const saved = { ...fresh, session_idle_timeout_seconds: 7_200, password_min_length: 16 };
    const server = fakeServer({ "PUT /instance/auth-settings": { status: 200, body: saved } });
    await renderWithRouter(() => <AuthSettingsForm settings={fresh} />);

    await retype("Session idle timeout", "7200");
    const user = await retype("Password minimum length", "16");
    expect(screen.getByRole("region", { name: "Unsaved changes" })).toBeVisible();
    await user.click(screen.getByLabelText("Unit of the session idle timeout"));
    await user.click(await screen.findByRole("option", { name: "seconds" }));
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(puts(server)).toHaveLength(1));
    expect(await puts(server)[0]!.json()).toEqual(saved);
    await waitFor(() =>
      expect(screen.queryByRole("region", { name: "Unsaved changes" })).not.toBeInTheDocument(),
    );
    expect(screen.getByLabelText("Session idle timeout")).toHaveValue("2");
  });

  it("puts every edit back on discard", async () => {
    fakeServer({});
    await renderWithRouter(() => <AuthSettingsForm settings={fresh} />);

    const user = await retype("Password minimum length", "20");
    await user.click(screen.getByRole("button", { name: "Discard" }));

    expect(screen.getByLabelText("Password minimum length")).toHaveValue("12");
    expect(screen.queryByRole("region", { name: "Unsaved changes" })).not.toBeInTheDocument();
  });

  it("refuses an idle timeout longer than the absolute one before asking the server", async () => {
    const server = fakeServer({});
    await renderWithRouter(() => <AuthSettingsForm settings={fresh} />);

    const user = await retype("Session idle timeout", "31");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(
      await screen.findByText("The idle timeout cannot be longer than the absolute timeout."),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Session idle timeout")).toHaveAttribute("aria-invalid", "true");
    expect(puts(server)).toHaveLength(0);
  });

  it("says the server's refusal of a field under it, a duration's bounds as spans", async () => {
    fakeServer({
      ...providers,
      "PUT /instance/auth-settings": problem(422, "invalid_auth_settings", {
        errors: [
          {
            pointer: "/token_max_lifetime_seconds",
            code: "out_of_range",
            detail: "",
            params: { min: 60, max: 315_360_000 },
          },
        ],
      }),
    });
    await renderWithRouter(() => <AuthSettingsForm settings={fresh} />);

    const user = await retype("Maximum token lifetime", "400");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(
      await screen.findByText("Enter a duration from 1 minute to 3,650 days."),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Maximum token lifetime")).toHaveAttribute("aria-invalid", "true");
    expect(
      screen.getByRole("combobox", { name: "Unit of the maximum token lifetime" }),
    ).toHaveAccessibleDescription(/Enter a duration from 1 minute to 3,650 days\./);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();

    // The field is being corrected: the server's word on what it held goes.
    await retype("Maximum token lifetime", "300");
    expect(
      screen.queryByText("Enter a duration from 1 minute to 3,650 days."),
    ).not.toBeInTheDocument();
    expect(screen.getByLabelText("Maximum token lifetime")).not.toHaveAttribute(
      "aria-invalid",
      "true",
    );
  });

  it("switches self-service password reset off, saying it also takes an SMTP server", async () => {
    const saved = { ...fresh, self_service_password_reset: false };
    const server = fakeServer({ "PUT /instance/auth-settings": { status: 200, body: saved } });
    await renderWithRouter(() => <AuthSettingsForm settings={fresh} />);

    const toggle = screen.getByRole("switch", { name: "Self-service password reset" });
    expect(toggle).toBeChecked();
    expect(toggle).toHaveAccessibleDescription(/only while an SMTP server is configured/);
    const user = userEvent.setup();
    await user.click(toggle);
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(puts(server)).toHaveLength(1));
    expect(await puts(server)[0]!.json()).toEqual(saved);
  });

  describe("local login", () => {
    it("names the command that restores it, and the identity needed to switch it off", async () => {
      fakeServer(providers);
      await renderWithRouter(() => <AuthSettingsForm settings={fresh} />);

      const toggle = screen.getByRole("switch", { name: "Local login" });
      expect(toggle).toBeChecked();
      expect(toggle).toHaveAccessibleDescription(/flagnetic admin unlock/);
      expect(toggle).toHaveAccessibleDescription(/identity at an enabled provider/);
    });

    it("switches local login off", async () => {
      const saved = { ...fresh, local_login_enabled: false };
      const server = fakeServer({
        ...providers,
        "PUT /instance/auth-settings": { status: 200, body: saved },
      });
      await renderWithRouter(() => <AuthSettingsForm settings={fresh} />);

      const user = userEvent.setup();
      await user.click(screen.getByRole("switch", { name: "Local login" }));
      await user.click(screen.getByRole("button", { name: "Save" }));

      await waitFor(() => expect(server.requests.some((r) => r.method === "PUT")).toBe(true));
      const put = server.requests.find((r) => r.method === "PUT")!;
      expect(await put.json()).toEqual(saved);
    });

    it("explains that the admin needs an identity of their own to switch it off", async () => {
      fakeServer({
        ...providers,
        "PUT /instance/auth-settings": problem(422, "identity_required"),
      });
      await renderWithRouter(() => <AuthSettingsForm settings={fresh} />);

      const user = userEvent.setup();
      await user.click(screen.getByRole("switch", { name: "Local login" }));
      await user.click(screen.getByRole("button", { name: "Save" }));

      expect(await screen.findByRole("alert")).toHaveTextContent(
        "an identity of your own at an enabled auth provider",
      );
    });
  });

  describe("enforced domains", () => {
    it("says when none is enforced", async () => {
      fakeServer(providers);
      await renderWithRouter(() => <AuthSettingsForm settings={fresh} />);

      expect(await screen.findByText("No domain is enforced.")).toBeVisible();
      expect(screen.getByRole("button", { name: "Add enforced domain" })).toBeEnabled();
    });

    it("enforces a domain to a provider that vouches for it, offering no other", async () => {
      const row = { domain: "shared.example", provider: "globex" };
      const saved = { ...fresh, sso_enforced_domains: [row] };
      const server = fakeServer({
        ...providers,
        "PUT /instance/auth-settings": { status: 200, body: saved },
      });
      await renderWithRouter(() => <AuthSettingsForm settings={fresh} />);

      const user = userEvent.setup();
      await user.click(await screen.findByRole("button", { name: "Add enforced domain" }));
      await user.click(screen.getByRole("combobox", { name: "Domain 1" }));
      // Only the domains of enabled providers are offered.
      expect(screen.queryByRole("option", { name: "dormant.example" })).not.toBeInTheDocument();
      await user.click(await screen.findByRole("option", { name: "shared.example" }));
      await user.click(screen.getByRole("combobox", { name: "Provider 1" }));
      expect(await screen.findAllByRole("option")).toHaveLength(2);
      await user.click(screen.getByRole("option", { name: "Globex" }));
      await user.click(screen.getByRole("button", { name: "Save" }));

      await waitFor(() => expect(server.requests.some((r) => r.method === "PUT")).toBe(true));
      expect(await server.requests.find((r) => r.method === "PUT")!.json()).toEqual(saved);
    });

    it("offers only the providers that vouch for the domain chosen", async () => {
      fakeServer(providers);
      await renderWithRouter(() => <AuthSettingsForm settings={fresh} />);

      const user = userEvent.setup();
      await user.click(await screen.findByRole("button", { name: "Add enforced domain" }));
      await user.click(screen.getByRole("combobox", { name: "Domain 1" }));
      await user.click(await screen.findByRole("option", { name: "acme.example" }));
      await user.click(screen.getByRole("combobox", { name: "Provider 1" }));
      expect(await screen.findAllByRole("option")).toHaveLength(1);
      expect(screen.getByRole("option", { name: "Acme SSO" })).toBeVisible();
    });

    it("asks for the domain and the provider of a row before sending anything", async () => {
      const server = fakeServer(providers);
      await renderWithRouter(() => <AuthSettingsForm settings={fresh} />);

      const user = userEvent.setup();
      await user.click(await screen.findByRole("button", { name: "Add enforced domain" }));
      await user.click(screen.getByRole("button", { name: "Save" }));

      expect(await screen.findByText("Choose a domain.")).toBeVisible();
      expect(screen.getByText("Choose a provider.")).toBeVisible();
      expect(server.requests.some((r) => r.method === "PUT")).toBe(false);
    });

    it("lists the rows already enforced and removes one", async () => {
      const enforced = {
        ...fresh,
        sso_enforced_domains: [
          { domain: "acme.example", provider: "acme" },
          { domain: "shared.example", provider: "globex" },
        ],
      };
      const server = fakeServer({
        ...providers,
        "PUT /instance/auth-settings": {
          status: 200,
          body: { ...enforced, sso_enforced_domains: [enforced.sso_enforced_domains[1]] },
        },
      });
      await renderWithRouter(() => <AuthSettingsForm settings={enforced} />);

      expect(await screen.findByRole("combobox", { name: "Domain 1" })).toHaveTextContent(
        "acme.example",
      );
      expect(screen.getByRole("combobox", { name: "Provider 2" })).toHaveTextContent("Globex");
      const user = userEvent.setup();
      await user.click(screen.getByRole("button", { name: "Remove enforced domain 1" }));
      await user.click(screen.getByRole("button", { name: "Save" }));

      await waitFor(() => expect(server.requests.some((r) => r.method === "PUT")).toBe(true));
      const sent = (await server.requests.find((r) => r.method === "PUT")!.json()) as {
        sso_enforced_domains: unknown;
      };
      expect(sent.sso_enforced_domains).toEqual([{ domain: "shared.example", provider: "globex" }]);
    });

    it("explains why the provider of a saved row was refused", async () => {
      const enforced = {
        ...fresh,
        sso_enforced_domains: [{ domain: "shared.example", provider: "acme" }],
      };
      fakeServer({
        ...providers,
        "PUT /instance/auth-settings": problem(422, "invalid_auth_settings", {
          errors: [
            { pointer: "/sso_enforced_domains/0/provider", code: "provider_disabled", detail: "" },
          ],
        }),
      });
      await renderWithRouter(() => <AuthSettingsForm settings={enforced} />);

      const user = userEvent.setup();
      // Changing the provider makes the form dirty so it can be saved.
      await user.click(await screen.findByRole("combobox", { name: "Provider 1" }));
      await user.click(await screen.findByRole("option", { name: "Globex" }));
      await user.click(screen.getByRole("button", { name: "Save" }));

      expect(
        await screen.findByText("This provider is disabled: enable it first, or choose another."),
      ).toBeVisible();
      expect(screen.getByRole("combobox", { name: "Provider 1" })).toHaveAttribute(
        "aria-invalid",
        "true",
      );
    });
  });
});
