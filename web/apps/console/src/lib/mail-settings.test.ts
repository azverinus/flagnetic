import { i18n } from "@lingui/core";
import { describe, expect, it } from "vitest";

import { activateForTests } from "@/test/render";

import { ApiError } from "./errors";
import { bodyOf, formOf, type MailForm, mailFormSchema, mailProblem } from "./mail-settings";

const stored = {
  host: "smtp.example.com",
  port: 465,
  security: "tls" as const,
  username: "relay",
  password_set: true,
  from: "Flagnetic <flags@example.com>",
  updated_at: "2026-11-01T10:00:00Z",
};

function issues(values: MailForm, passwordSet = false) {
  const result = mailFormSchema(i18n, {
    stored: passwordSet ? { ...stored, port: 587, security: "starttls" } : null,
  }).safeParse(values);
  return result.success
    ? {}
    : Object.fromEntries(result.error.issues.map((issue) => [issue.path.join("."), issue.message]));
}

const valid: MailForm = {
  host: "smtp.example.com",
  port: "587",
  security: "starttls",
  username: "",
  password: "",
  from: "flags@example.com",
};

describe("the mail settings form", () => {
  it("starts on the submission port with STARTTLS when nothing is configured", () => {
    expect(formOf(null)).toEqual({
      host: "",
      port: "587",
      security: "starttls",
      username: "",
      password: "",
      from: "",
    });
  });

  it("shows what is stored, never a password", () => {
    expect(formOf(stored)).toEqual({
      host: "smtp.example.com",
      port: "465",
      security: "tls",
      username: "relay",
      password: "",
      from: "Flagnetic <flags@example.com>",
    });
  });

  it("sends a typed password and leaves an empty one out, which keeps the stored one", () => {
    expect(bodyOf({ ...formOf(stored), password: "" })).toEqual({
      host: "smtp.example.com",
      port: 465,
      security: "tls",
      username: "relay",
      from: "Flagnetic <flags@example.com>",
    });
    expect(bodyOf({ ...formOf(stored), password: " new pass " }).password).toBe(" new pass ");
  });

  it("trims what is typed around the host, the username and the sender", () => {
    expect(
      bodyOf({ ...valid, host: " smtp.example.com ", username: " relay ", from: " a@b.test " }),
    ).toMatchObject({ host: "smtp.example.com", username: "relay", from: "a@b.test" });
  });

  it("drops the password with the username, so clearing the username removes both", () => {
    expect(bodyOf({ ...formOf(stored), username: "", password: "" })).toEqual({
      host: "smtp.example.com",
      port: 465,
      security: "tls",
      username: "",
      from: "Flagnetic <flags@example.com>",
    });
  });
});

describe("the mail settings form's checks", () => {
  activateForTests("en");

  it("accepts a server without credentials", () => {
    expect(issues(valid)).toEqual({});
  });

  it("asks for a host, a sender and a port in range", () => {
    expect(issues({ ...valid, host: " ", from: "", port: "0" })).toEqual({
      host: "Enter the SMTP server's host name or address.",
      from: "Enter the sender's address.",
      port: "Enter a value from 1 to 65,535.",
    });
    expect(issues({ ...valid, port: "25a" })).toEqual({ port: "Enter a value from 1 to 65,535." });
  });

  it("refuses a host with spaces as the server would", () => {
    expect(issues({ ...valid, host: "smtp example.com" })).toEqual({
      host: "Enter a host name or address of at most 253 characters, without spaces.",
    });
  });

  it("asks for a password with a username unless one is stored", () => {
    expect(issues({ ...valid, username: "relay" })).toEqual({
      password: "A username needs a password.",
    });
    expect(issues({ ...valid, username: "relay" }, true)).toEqual({});
  });

  it("asks for the stored password again when the host, port, security or username moves", () => {
    const kept = { ...valid, username: "relay" };
    for (const moved of [
      { host: "other.example.net" },
      { port: "2525" },
      { security: "tls" as const },
      { username: "other" },
    ]) {
      expect(issues({ ...kept, ...moved }, true)).toEqual({
        password: "A username needs a password.",
      });
      expect(issues({ ...kept, ...moved, password: "again" }, true)).toEqual({});
    }
    expect(issues({ ...kept, host: "SMTP.example.com", from: "other@example.com" }, true)).toEqual(
      {},
    );
  });

  it("asks for a username with a password", () => {
    expect(issues({ ...valid, password: "secret" })).toEqual({
      username: "A password needs a username.",
    });
  });
});

describe("a refusal of the mail settings", () => {
  activateForTests("en");

  const failed = (failure?: string) =>
    new ApiError(502, { code: "smtp_failed", ...(failure ? { failure } : {}) });

  it("explains each step at which the test mail failed", () => {
    expect(mailProblem(i18n, failed("connection"))).toMatch(/could not reach the SMTP server/);
    expect(mailProblem(i18n, failed("tls"))).toMatch(/secure connection .* failed/);
    expect(mailProblem(i18n, failed("authentication"))).toMatch(
      /refused the username and password/,
    );
    expect(mailProblem(i18n, failed("rejected"))).toMatch(/refused the sender, the recipient/);
    expect(mailProblem(i18n, failed())).toMatch(/did not accept the test mail/);
  });

  it("says the public address is what links are built from", () => {
    expect(mailProblem(i18n, new ApiError(422, { code: "base_url_required" }))).toMatch(
      /FLAGNETIC_BASE_URL.*link/,
    );
  });

  it("leaves any other refusal to the general sentences", () => {
    expect(mailProblem(i18n, new ApiError(403, { code: "forbidden" }))).toBe(
      "You may not do this.",
    );
  });
});
