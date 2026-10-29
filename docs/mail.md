# Mail

An instance admin configures one SMTP server, and the instance sends its mail through it: today a test mail to whoever asks for one, and later the invitations and password resets that would otherwise be shown as links. Mail is optional. Without an SMTP server nothing is mailed and every link is shown to the admin who made it.

## Configuring the SMTP server

`PUT /instance/smtp` sets the server; `GET /instance/smtp` reads it and `DELETE /instance/smtp` removes it (both answer `404 not_found` while none is configured). All four operations, with `POST /instance/smtp/test`, are an instance admin's. The console's **Mail** screen does the same.

| Field | |
|---|---|
| `host` | A host name or address, 1 to 253 characters without spaces. |
| `port` | 1 to 65535. Usually 587 with `starttls`, 465 with `tls`, 25 or 1025 with `none`. |
| `security` | `starttls`, `tls` or `none`; see [TLS modes](#tls-modes). |
| `username`, `password` | Optional; give both or neither. |
| `from` | The sender of every mail: one address, optionally with a display name, such as `Flagnetic <flags@example.com>`. |

A setting that breaks a rule is refused `422 invalid_smtp_settings` with a violation at each field to fix. Credentials over `security: none` are accepted only for a host that is this machine (`localhost`, `127.0.0.1`, `::1`), since the client never sends a password in the clear to another host.

`FLAGNETIC_BASE_URL` must be set before the SMTP server is: every mailed link is built from it, and a mail carrying a link to nowhere helps nobody. Without it `PUT` is refused `422 base_url_required`.

The password is sealed under the instance's secret key (see [The secret key](operations.md#the-secret-key)) and is write only: `GET` answers `password_set` and never the password. Leave `password` out of a `PUT` to keep the stored one. Leave the `username` out (or empty) to remove the credentials, the stored password with them. A `PUT` that leaves the settings as they are changes and records nothing.

The change is recorded as `smtp.updated` with the settings before and after, and removal as `smtp.deleted`; the password appears only as `password_set`. Every replica applies a change within a second, through the `smtp_settings` notification channel; while that listener is down a replica reads the settings from PostgreSQL on every use.

## TLS modes

- `starttls` connects in plain text and upgrades the connection. The server must offer STARTTLS: a server that does not is refused, never used in the clear.
- `tls` speaks TLS from the first byte (often port 465).
- `none` secures nothing. Use it for a relay on the same machine or inside a network you trust.

Both secure modes require TLS 1.2 or later and verify the server's certificate against the system's roots and the `host` you gave: a certificate for another name, an expired one or one from an authority the system does not trust fails as `tls`. Sending times out after 10 seconds to connect and 30 seconds in all. Authentication uses the best mechanism the server offers among PLAIN, LOGIN and CRAM-MD5; PLAIN and LOGIN are used only over TLS or to this machine.

## The test mail

`POST /instance/smtp/test` sends a mail to the caller's own address, in their language, and answers once the server accepted it: `204`. It is the one mail the instance sends while the caller waits, so a configuration problem shows at once.

- `409 smtp_not_configured`: there is no SMTP server yet.
- `502 smtp_failed`, with `failure` naming the step that failed:
  - `connection`: the server could not be reached, or the connection broke or timed out.
  - `tls`: STARTTLS was not offered, the handshake failed, or the certificate was not trusted.
  - `authentication`: the server refused the username and password, or offered no mechanism the connection may use.
  - `rejected`: the server refused the sender, the recipient or the mail.

The answer never carries the password.

## Languages

Mail is written as text and as HTML, in English or Russian. The language is the recipient's `locale`; when they have not chosen one, `FLAGNETIC_DEFAULT_LOCALE`. An invitation to a person the instance does not know yet is written in the language of whoever invited them, then the default.

## What happens when a send fails

Mail is sent after the change that causes it has been stored, in the background, and never queued: a slow or failing relay never delays the change nor undoes it, and nothing retries a mail the relay refused. A mail in flight when a replica stops is awaited for up to a minute, and each mail is given up after a minute. A failure is logged with the request ID, the kind of mail and the failure class (never the link) and counted in `flagnetic_mail_sent_total` (see [Metrics](operations.md#metrics)). Each kind of mail has a cheap human retry: send the test mail again, re-issue the invitation.
