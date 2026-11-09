import {
  deleteMailSettings,
  getMailSettings,
  sendTestMail,
  type SmtpSecurity,
  type SmtpSettings,
  type SmtpSettingsUpdateWritable,
  updateMailSettings,
} from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";
import { queryOptions } from "@tanstack/react-query";
import { z } from "zod";

import { ApiError, unwrap } from "./errors";
import { problemMessage, violationMessage } from "./problem-messages";

/**
 * The instance's SMTP server, or null while none is configured: the server
 * answers 404 then, which here is a state of the page, not an error.
 */
export const mailSettingsQuery = queryOptions<SmtpSettings | null>({
  queryKey: ["instance", "smtp"],
  queryFn: async () => {
    try {
      return await unwrap(getMailSettings());
    } catch (error) {
      if (error instanceof ApiError && error.status === 404) return null;
      throw error;
    }
  },
});

/** The operations on the SMTP server, through the generated client. */
export const mailSettingsApi = {
  save: (body: SmtpSettingsUpdateWritable) => unwrap(updateMailSettings({ body })),
  remove: () => unwrap(deleteMailSettings()),
  test: () => unwrap(sendTestMail()),
};

/** The security modes, in the order the form offers them. */
export const securities = ["starttls", "tls", "none"] as const satisfies readonly SmtpSecurity[];

/** The SMTP settings as their form holds them; the port is what was typed. */
export type MailForm = {
  host: string;
  port: string;
  security: SmtpSecurity;
  username: string;
  /** Write-only: empty keeps the stored password. */
  password: string;
  from: string;
};

/**
 * The form for the stored settings, never carrying a password, or a blank
 * one on the submission port with STARTTLS, which most relays expect.
 */
export function formOf(settings: SmtpSettings | null): MailForm {
  return {
    host: settings?.host ?? "",
    port: String(settings?.port ?? 587),
    security: settings?.security ?? "starttls",
    username: settings?.username ?? "",
    password: "",
    from: settings?.from ?? "",
  };
}

/**
 * The request a valid form sends. A password goes only when one was typed,
 * so an empty field keeps the stored one; with no username the password is
 * left out too, and the server drops the stored one with the username.
 */
export function bodyOf(values: MailForm): SmtpSettingsUpdateWritable {
  const username = values.username.trim();
  return {
    host: values.host.trim(),
    port: Number(values.port.trim()),
    security: values.security,
    username,
    ...(username !== "" && values.password !== "" ? { password: values.password } : {}),
    from: values.from.trim(),
  };
}

/** The JSON Pointer of each field of the form in the request body. */
export const pointerOf = {
  host: "/host",
  port: "/port",
  security: "/security",
  username: "/username",
  password: "/password",
  from: "/from",
} as const satisfies Record<keyof MailForm, string>;

const maxHostLength = 253;
const portBounds = { min: 1, max: 65_535 } as const;

/**
 * The form's checks before it asks the server, in the words the server's
 * violations are shown in. Whether the sender is one address, and whether
 * credentials may travel without encryption to the host, are left to the
 * server, which parses addresses and knows which hosts are this machine.
 */
export function mailFormSchema(i18n: I18n, { passwordSet }: { passwordSet: boolean }) {
  const violation = (field: keyof MailForm, code: string, params?: Record<string, unknown>) =>
    violationMessage(i18n, { pointer: pointerOf[field], code, detail: "", params });
  return z
    .object({
      host: z.string(),
      port: z.string(),
      security: z.enum(securities),
      username: z.string(),
      password: z.string(),
      from: z.string(),
    })
    .superRefine((values, context) => {
      const issue = (field: keyof MailForm, message: string) =>
        context.addIssue({ code: "custom", path: [field], message });
      const host = values.host.trim();
      if (host === "") {
        issue("host", i18n._(msg`Enter the SMTP server's host name or address.`));
      } else if (host.length > maxHostLength || /\s/.test(host)) {
        issue("host", violation("host", "invalid_host"));
      }
      const port = values.port.trim();
      if (!/^\d+$/.test(port) || Number(port) < portBounds.min || Number(port) > portBounds.max) {
        issue("port", violation("port", "out_of_range", portBounds));
      }
      if (values.from.trim() === "") {
        issue("from", i18n._(msg`Enter the sender's address.`));
      }
      const username = values.username.trim();
      if (username !== "" && values.password === "" && !passwordSet) {
        issue("password", violation("password", "credentials_together"));
      } else if (username === "" && values.password !== "") {
        issue("username", violation("username", "credentials_together"));
      }
    });
}

/**
 * The sentence for a refusal of the mail settings or the test mail. A test
 * mail that failed says at which step, since each points at different
 * settings; the public address is said to be what mailed links are built
 * from. Any other refusal reads as everywhere else.
 */
export function mailProblem(i18n: I18n, error: ApiError): string {
  switch (error.code) {
    case "smtp_failed":
      switch (error.problem.failure) {
        case "connection":
          return i18n._(
            msg`The instance could not reach the SMTP server, or the connection broke. Check the host and the port, and that the server accepts connections from the instance.`,
          );
        case "tls":
          return i18n._(
            msg`The secure connection to the SMTP server failed: it does not offer STARTTLS, or the TLS handshake or its certificate failed. Check the security and the port.`,
          );
        case "authentication":
          return i18n._(
            msg`The SMTP server refused the username and password. Check them, then save and test again.`,
          );
        case "rejected":
          return i18n._(
            msg`The SMTP server refused the sender, the recipient or the mail. Check that the server accepts mail from the sender address and to your address.`,
          );
        default:
          return i18n._(msg`The SMTP server did not accept the test mail.`);
      }
    case "smtp_not_configured":
      return i18n._(msg`No SMTP server is configured. Save the settings first.`);
    case "base_url_required":
      return i18n._(
        msg`Set the instance's public address first (FLAGNETIC_BASE_URL): every link the instance mails is built from it.`,
      );
    default:
      return problemMessage(i18n, error);
  }
}
