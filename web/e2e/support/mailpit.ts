import { execFile } from "node:child_process";
import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { promisify } from "node:util";

import { deleteMailSettings, updateMailSettings } from "@flagnetic/api";

import { managementClient, must } from "./console";
import { freePort, workDir } from "./instance";

const run = promisify(execFile);

/** The Mailpit release the mail specs send through. */
const image = "axllent/mailpit:v1.32.0";

const statePath = resolve(workDir, "mailpit.json");

/**
 * The credentials the run's Mailpit asks for. It takes them over a plain
 * connection, which the instance allows only to a server on this machine,
 * as Mailpit is. They guard a throwaway container.
 */
export const mailpitUser = "relay";
export const mailpitPassword = "mailpit-relay-pass-4417";

export type MailpitState = {
  /** Where its SMTP server listens: loopback, so credentials may travel in the clear. */
  smtpHost: string;
  smtpPort: number;
  /** The base URL of its HTTP interface. */
  api: string;
  container: string;
};

export function readMailpit(): MailpitState {
  return JSON.parse(readFileSync(statePath, "utf8")) as MailpitState;
}

/**
 * Starts Mailpit in Docker, its SMTP and HTTP ports bound to loopback, and
 * waits until it answers. It takes mail only from {@link mailpitUser} and
 * keeps what it receives for {@link mailsTo} to read. One container serves
 * the whole run; `stopMailpit` removes it.
 */
export async function startMailpit(): Promise<MailpitState> {
  const [smtpPort, httpPort] = [await freePort(), await freePort()];
  const container = `flagnetic-e2e-mailpit-${process.pid}`;
  try {
    await run("docker", [
      "run",
      "--detach",
      "--rm",
      "--name",
      container,
      "--publish",
      `127.0.0.1:${smtpPort}:1025`,
      "--publish",
      `127.0.0.1:${httpPort}:8025`,
      "--env",
      `MP_SMTP_AUTH=${mailpitUser}:${mailpitPassword}`,
      "--env",
      "MP_SMTP_AUTH_ALLOW_INSECURE=true",
      image,
    ]);
  } catch (error) {
    throw new Error(
      `starting Mailpit needs Docker: ${error instanceof Error ? error.message : String(error)}`,
      { cause: error },
    );
  }
  const state: MailpitState = {
    smtpHost: "127.0.0.1",
    smtpPort,
    api: `http://127.0.0.1:${httpPort}`,
    container,
  };
  mkdirSync(workDir, { recursive: true });
  writeFileSync(statePath, JSON.stringify(state, null, 2));

  const deadline = Date.now() + 60_000;
  for (;;) {
    try {
      const response = await fetch(`${state.api}/api/v1/info`);
      if (response.ok) return state;
    } catch {
      // Not listening yet.
    }
    if (Date.now() > deadline) {
      await stopMailpit();
      throw new Error(`Mailpit was not ready at ${state.api}`);
    }
    await new Promise((done) => setTimeout(done, 200));
  }
}

/** Removes the container the run started, if it started one. */
export async function stopMailpit(): Promise<void> {
  let state: MailpitState;
  try {
    state = readMailpit();
  } catch {
    return;
  }
  rmSync(statePath, { force: true });
  try {
    await run("docker", ["rm", "--force", state.container]);
  } catch {
    // Already gone.
  }
}

/** One mail Mailpit received, as a test reads it. */
export type Mail = {
  id: string;
  subject: string;
  from: string;
  to: string[];
  text: string;
  html: string;
};

type Address = { Address: string; Name: string };
type Summary = { ID: string };
type Message = {
  ID: string;
  Subject: string;
  From: Address;
  To: Address[] | null;
  Text: string;
  HTML: string;
};

/** The search that finds every mail to recipient. */
function toRecipient(recipient: string): string {
  return `query=${encodeURIComponent(`to:"${recipient}"`)}`;
}

async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${readMailpit().api}/api/v1/${path}`, init);
  if (!response.ok) {
    throw new Error(`Mailpit answered ${response.status} to ${path}: ${await response.text()}`);
  }
  return (await response.json()) as T;
}

/** Every mail to recipient Mailpit holds, newest first. */
export async function mailsTo(recipient: string): Promise<Mail[]> {
  const found = await api<{ messages: Summary[] | null }>(`search?${toRecipient(recipient)}`);
  return Promise.all(
    (found.messages ?? []).map(async ({ ID }) => {
      const message = await api<Message>(`message/${ID}`);
      return {
        id: message.ID,
        subject: message.Subject,
        from: message.From.Address,
        to: (message.To ?? []).map((to) => to.Address),
        text: message.Text,
        html: message.HTML,
      };
    }),
  );
}

/**
 * The newest mail to recipient, waiting for the first to arrive. A mail
 * the instance sends after its answer may land a moment later.
 */
export async function awaitMailTo(recipient: string, timeout = 10_000): Promise<Mail> {
  const deadline = Date.now() + timeout;
  for (;;) {
    const [newest] = await mailsTo(recipient);
    if (newest) return newest;
    if (Date.now() > deadline) throw new Error(`no mail to ${recipient} arrived in ${timeout} ms`);
    await new Promise((done) => setTimeout(done, 200));
  }
}

/**
 * Forgets every mail to recipient, so a spec that mails an address the
 * suite shares reads only what it sent itself.
 */
export async function clearMailsTo(recipient: string): Promise<void> {
  const response = await fetch(`${readMailpit().api}/api/v1/search?${toRecipient(recipient)}`, {
    method: "DELETE",
  });
  if (!response.ok) {
    throw new Error(`Mailpit answered ${response.status} to deleting the mails to ${recipient}`);
  }
}

/** The sender the instance mails from in the specs. */
export const mailSender = "Flagnetic <flags@flagnetic.test>";

/**
 * Points the instance at the run's Mailpit through the management API.
 * `password` defaults to the one Mailpit takes; another makes every send
 * fail at authentication.
 */
export async function configureSmtp(password: string = mailpitPassword): Promise<void> {
  const mailpit = readMailpit();
  must(
    "configuring SMTP",
    await updateMailSettings({
      client: managementClient(),
      body: {
        host: mailpit.smtpHost,
        port: mailpit.smtpPort,
        security: "none",
        username: mailpitUser,
        password,
        from: mailSender,
      },
    }),
  );
}

/**
 * Removes the instance's SMTP server, if it has one. A spec that configures
 * one removes it after itself: with SMTP, the instance mails what the specs
 * after it expect to be shown, such as invitation links.
 */
export async function removeSmtp(): Promise<void> {
  const removed = await deleteMailSettings({ client: managementClient() });
  if (removed.error !== undefined && removed.response?.status !== 404) {
    throw new Error(`removing SMTP failed: ${JSON.stringify(removed.error)}`);
  }
}
