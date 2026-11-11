# Mail

An instance admin configures one SMTP server, and the instance sends its mail through it: a test mail to whoever asks for one, the password reset a member asks for, and every invitation, which would otherwise be shown as a link. Mail is optional. Without an SMTP server nothing is mailed and every link is shown to whoever made it.

## Configuring the SMTP server

In the console, an instance admin does it under **Mail**: the form sets the server, **Send a test mail** sends the [test mail](#the-test-mail) to their own address and explains a failure, and **Remove** removes the server. The password field is always empty; a stored password is kept until a new one is typed, though changing the host, port, security or username asks for it again, and clearing the username removes both.

Through the API, `PUT /instance/smtp` sets the server; `GET /instance/smtp` reads it and `DELETE /instance/smtp` removes it (both answer `404 not_found` while none is configured). All four operations, with `POST /instance/smtp/test`, are an instance admin's.

| Field | |
|---|---|
| `host` | A host name or address, 1 to 253 characters without spaces. |
| `port` | 1 to 65535. Usually 587 with `starttls`, 465 with `tls`, 25 or 1025 with `none`. |
| `security` | `starttls`, `tls` or `none`; see [TLS modes](#tls-modes). |
| `username`, `password` | Optional; give both or neither. |
| `from` | The sender of every mail: one address, optionally with a display name, such as `Flagnetic <flags@example.com>`. |

A setting that breaks a rule is refused `422 invalid_smtp_settings` with a violation at each field to fix. Credentials over `security: none` are accepted only for a host that is this machine (`localhost`, `127.0.0.1`, `::1`), since the client never sends a password in the clear to another host.

`FLAGNETIC_BASE_URL` must be set before the SMTP server is: every mailed link is built from it, and a mail carrying a link to nowhere helps nobody. Without it `PUT` is refused `422 base_url_required`.

The password is sealed under the instance's secret key (see [The secret key](operations.md#the-secret-key)) and is write only: `GET` answers `password_set` and never the password. Leave `password` out of a `PUT` to keep the stored one, as long as the `host`, `port`, `security` and `username` are unchanged: a `PUT` that moves any of them without a `password` is refused `422 invalid_smtp_settings` with `credentials_together` at `/password`, so the stored password is never sent to a server it was not given for. Leave the `username` out (or empty) to remove the credentials, the stored password with them. A `PUT` that leaves the settings as they are changes and records nothing.

The change is recorded as `smtp.updated` with the settings before and after, and removal as `smtp.deleted`; the password appears only as `password_set`. Every replica applies a change within a second, through the `smtp_settings` notification channel; while that listener is down a replica reads the settings from PostgreSQL on every use.

## TLS modes

- `starttls` connects in plain text and upgrades the connection. The server must offer STARTTLS: a server that does not is refused, never used in the clear.
- `tls` speaks TLS from the first byte (often port 465).
- `none` secures nothing. Use it for a relay on the same machine or inside a network you trust.

Both secure modes require TLS 1.2 or later and verify the server's certificate against the system's roots and the `host` you gave: a certificate for another name, an expired one or one from an authority the system does not trust fails as `tls`. Sending times out after 10 seconds to connect and 30 seconds for the whole exchange after that. Authentication uses the best mechanism the server offers among PLAIN, LOGIN and CRAM-MD5; PLAIN and LOGIN are used only over TLS or to this machine.

## The test mail

`POST /instance/smtp/test` sends a mail to the caller's own address, in their language, and answers once the server accepted it: `204`. It is the one mail the instance sends while the caller waits, so a configuration problem shows at once.

- `409 smtp_not_configured`: there is no SMTP server yet.
- `502 smtp_failed`, with `failure` naming the step that failed:
  - `connection`: the server could not be reached, or the connection broke or timed out.
  - `tls`: STARTTLS was not offered, the handshake failed, or the certificate was not trusted.
  - `authentication`: the server refused the username and password, or offered no mechanism the connection may use.
  - `rejected`: the server refused the sender, the recipient or the mail.

The answer never carries the password.

## Setting up common relays

Every relay asks for the same five things: a host, a port, a [TLS mode](#tls-modes), credentials and a sender the relay accepts. Check the values below against your provider's own documentation, which changes more often than this page. After saving, send the [test mail](#the-test-mail).

| Relay | `host` | `port` and `security` | `username`, `password` | `from` |
|---|---|---|---|---|
| A relay on the same machine or network (Postfix, Exim, a company relay that trusts your address) | its name or address | 25 with `none`, or 587 with `starttls` | none, when it trusts you by address | any address the relay lets you send as |
| Amazon SES | `email-smtp.<region>.amazonaws.com` | 587 with `starttls`, or 465 with `tls` | the SMTP credentials created in the SES console, which are not an IAM access key | an address or domain verified in SES; a new SES account may send only to verified recipients until its sandbox is lifted |
| SendGrid | `smtp.sendgrid.net` | 587 with `starttls` | the username `apikey` and an API key allowed to send mail | a verified sender or domain |
| Mailgun | `smtp.mailgun.org` | 587 with `starttls` | the SMTP credentials of the sending domain, `postmaster@<domain>` by default | an address of that domain |
| Postmark | `smtp.postmarkapp.com` | 587 with `starttls` | the server's token as both username and password | a confirmed sender signature or domain |
| Google Workspace or Gmail | `smtp.gmail.com` | 587 with `starttls`, or 465 with `tls` | the mailbox and an app password (the account needs two-step verification) | the mailbox's own address, or an alias it may send as |
| Microsoft 365 | `smtp.office365.com` | 587 with `starttls` | the mailbox and its password, with authenticated SMTP allowed for the mailbox | the mailbox's own address |
| Mailpit, for a trial | its name or address | 1025 with `none` | none | any address |

A relay that offers only a password over plain text on a network you do not control is not a relay to use: choose `starttls` or `tls`. The instance refuses credentials over `none` to any host that is not this machine.

Sending as an address on your own domain works best when the domain publishes SPF and DKIM records that name the relay: a mail the relay accepts can still land in a recipient's spam folder without them, which no setting here can show.

## Troubleshooting

Start from what the test mail says. A `502 smtp_failed` carries a `failure`; the same classes label `flagnetic_mail_sent_total` and the server's log record for a mail that failed later.

| Symptom | Cause and what to do |
|---|---|
| `422 invalid_smtp_settings` | A field breaks a rule; the violations name each one. The common one is a username over `security: none` to a host that is not this machine: switch to `starttls` or `tls`. |
| `422 base_url_required` | `FLAGNETIC_BASE_URL` is not set. Set it to the address members open the console at and restart. |
| `409 smtp_not_configured` | There is no SMTP server yet, or it was removed. |
| `failure: connection` | The host or port is wrong, a firewall or the cloud provider blocks outbound SMTP (many block port 25, and some every port until you ask), the relay is down, or it did not accept the connection within 10 seconds or finish the exchange within 30. Try another port the relay offers, such as 587 or 2525. Test from the instance's own network: `nc -vz <host> <port>`. |
| `failure: tls` | The mode does not match the port (`tls` on 587, `starttls` on 465), the relay does not offer STARTTLS, its certificate is for another name than `host`, has expired or comes from an authority the instance's system does not trust, or the relay only speaks TLS 1.0 or 1.1. Give the host name the certificate names, not an address. |
| `failure: authentication` | The username or password is wrong, the relay needs an app password or an API key rather than the account password, authenticated SMTP is off for the account, or the relay offers only a mechanism the connection may not use (PLAIN and LOGIN need TLS). The password is sealed and never shown; type it again. |
| `failure: rejected` | The relay refused the sender, the recipient or the mail itself. Usually `from` is not an address the account may send as, the domain is not verified at the relay, or the relay is in a sandbox that allows only some recipients. The answer does not carry the relay's own message; the server's log record of the failure does, under `error`, as does the relay's log. |
| The test mail was accepted and never arrived | Look in the recipient's spam folder and at the relay's own delivery log. The instance reports only what the relay answered at the time. |
| An invitation shows a link instead of being mailed | No SMTP server is configured, or `FLAGNETIC_BASE_URL` is unset. The link is yours to hand over. |
| A password reset mail never comes | The instance mails it only while it has an SMTP server, only to an active member who holds a password, at most once in 5 minutes, and only while local login is on, self-service reset is allowed and the member's domain is not [enforced](sso.md#sso-enforcement-and-local-login); the answer is `202` either way so nobody learns which emails exist. |
| `flagnetic_mail_sent_total{result="error"}` rises | A mail failed before the relay was reached, such as a stored password that the current `FLAGNETIC_SECRET_KEY` cannot open. Type the password again, or restore the key. |

## What is mailed

| Kind | Sent to | When |
|---|---|---|
| `test` | the instance admin who asks | `POST /instance/smtp/test`; the one mail sent while the caller waits. |
| `invitation` | the person invited | An instance admin invites someone (`POST /members`), a member holding `grant.manage` invites someone to a project (`POST /projects/{project}/invitations`), an invitation is reissued (`POST /members/{member}/invitation`, or inviting a person to the same project again), or a member who never set a password is reactivated and so invited again. It names who invited (when someone did) and, for an invitation to a project, the project and the role accepting grants there, and carries a link to `FLAGNETIC_BASE_URL/invitations/<secret>` that works once for seven days, and when it expires in UTC. The answer says `mailed: true` and shows no link, so accepting proves the person holds the address; see [Inviting with mail](access.md#inviting-with-mail). |
| `password_reset` | a member | They ask for a reset with `POST /auth/password-reset`, the instance lets them (`self_service_password_reset`), and none was issued for them in the last 5 minutes; see [Asking for a password reset by mail](access.md#asking-for-a-password-reset-by-mail). It carries a link to `FLAGNETIC_BASE_URL/password-reset/<secret>` that works once for an hour, and when it expires in UTC. |
| `webhook_disabled` | the active members who manage webhooks in a project, or the instance admins | The platform disables a webhook subscription for failing persistently; see [When a subscription keeps failing](webhooks.md#when-a-subscription-keeps-failing). For a project's subscription: every active member whose grants give them `webhook.manage` there, instance admins included; for an instance subscription: every active instance admin. It names the subscription and its project (or the instance), says why it was disabled, and links to its page in the console, `FLAGNETIC_BASE_URL/projects/<project>/webhooks/<subscription>` or `FLAGNETIC_BASE_URL/admin/webhooks/<subscription>`. |

A password reset an instance admin issues is never mailed: its link is shown to them once. An invitation is mailed only while `FLAGNETIC_BASE_URL` is set, as every mailed link is built from it; an instance whose SMTP server outlived the setting shows the link instead, and a disabled webhook subscription is announced by its audit entry and the console banner alone.

## Languages

Mail is written as text and as HTML, in English or Russian. The language is the recipient's `locale`; when they have not chosen one, `FLAGNETIC_DEFAULT_LOCALE`. An invitation to a person the instance does not know yet is written in the language of whoever invited them, then the default.

## What happens when a send fails

Mail is sent after the change that causes it has been stored, in the background, and never queued: a slow or failing relay never delays the change nor undoes it, and nothing retries a mail the relay refused. A mail in flight when a replica stops is awaited for up to a minute, and each mail is given up after a minute. A failure is logged with the request ID, the kind of mail and the failure class (never the link) and counted in `flagnetic_mail_sent_total` (see [Metrics](operations.md#metrics)). A replica runs at most 256 mails and other background jobs at once; a mail started past that is not sent, but logged and counted in `flagnetic_background_dropped_total` instead. Each kind of mail has a cheap human retry: send the test mail again, re-issue the invitation.
