# Authentication and credential lifecycle

This document defines the implemented authentication contract for protected
routes. Kaven Media Server has one administrator identity and uses a password
login followed by an opaque, server-side session.

## First-run setup

A fresh zero-configuration instance opens a setup page before the normal UI.
The user chooses the single administrator username and a password of at least
16 characters. The server stores the username and a bcrypt password verifier
in SQLite, restarts its HTTP lifecycle, and the UI signs in with the new
credential. The plaintext password is never persisted or logged.

The setup endpoint uses an atomic single-row insert. The first successful
request permanently closes initialization, including when two requests race.
Until that happens, protected routes remain unavailable. Initialize a new
Internet-facing instance through a trusted network before exposing it publicly;
any client that can reach an uninitialized server can claim its administrator
account.

No reusable default administrator password is provided.

## Configuration contract

| Setting | Default | Purpose |
| --- | --- | --- |
| `KAVEN_ADMIN_USERNAME` | `admin` | Administrator login name |
| `KAVEN_ADMIN_PASSWORD_FILE` | unset | Absolute path to a mounted secret file; preferred |
| `KAVEN_ADMIN_PASSWORD` | unset | Direct environment fallback for development |
| `KAVEN_ADMIN_SECURE_COOKIE` | `false` | Force the session cookie's `Secure` flag behind a TLS-terminating proxy |

The server automatically marks the cookie `Secure` when it directly receives
HTTPS. Set `KAVEN_ADMIN_SECURE_COOKIE=true` when public HTTPS terminates at a
reverse proxy and the proxy connects to this server over HTTP. Do not enable it
for a plain-HTTP origin because browsers will then withhold the cookie.

There is no password command-line flag because process command lines are
commonly observable. The password-file and direct-password settings are
mutually exclusive. Password files must be absolute, readable, regular files.
One final line ending is removed; other whitespace remains part of the secret.
Passwords must be valid UTF-8, contain no control characters, and be 16-1024
bytes. Usernames must be 1-64 ASCII letters, digits, `.`, `_`, `@`, or `-`.

Environment credentials take precedence over the administrator created by the
setup page and are loaded again on every process start. This permits deployment
systems to manage the credential externally and provides an operator recovery
path. Removing the environment credential restores use of the initialized
SQLite credential.

The application pre-hashes passwords with SHA-256 before bcrypt. This supports
the full password length without retaining the original password. Setup stores
only the bcrypt verifier in SQLite. Environment-provided passwords produce an
in-memory verifier and are never persisted. Remembered sessions store only a
token digest, a credential-bound digest, and timestamps in SQLite.

For containers, mount a secret outside `/data` and set only its path:

```yaml
services:
  kaven-media:
    environment:
      KAVEN_ADMIN_PASSWORD_FILE: /run/secrets/kaven_admin_password
      KAVEN_ADMIN_SECURE_COOKIE: "true"
    secrets:
      - kaven_admin_password

secrets:
  kaven_admin_password:
    file: ./secrets/kaven_admin_password.txt
```

Do not commit the referenced host file or bake secrets into the image.

## HTTP initialization and authentication contract

The initialization status route is always available. The mutation succeeds
only before an administrator exists:

| Method | Route | Result |
| --- | --- | --- |
| `GET` | `/api/v1/setup/status` | Return `{"initialized": true|false}` |
| `POST` | `/api/v1/setup` | Atomically create the first administrator |

Setup accepts the same `username` and `password` JSON shape as login. Its body
is limited to 8 KiB, unknown fields are rejected, and browser requests require
a same-origin `Origin`. A repeated setup returns HTTP 409.

The browser session API is available after initialization:

| Method | Route | Result |
| --- | --- | --- |
| `POST` | `/api/v1/admin/login` | Validate JSON credentials and create a session |
| `GET` | `/api/v1/admin/session` | Return `authenticated: true` or `false`; refresh a valid session |
| `POST` | `/api/v1/admin/logout` | Revoke the current session and expire its cookie |
| `GET` | `/api/v1/admin/password` | Report whether the password is managed externally |
| `POST` | `/api/v1/admin/password` | Verify and replace a setup-managed password |
| `GET` | `/api/v1/admin/session-policy` | Read the remembered-login duration |
| `PUT` | `/api/v1/admin/session-policy` | Set the remembered-login duration from 1 to 365 days |
| `GET` | `/api/v1/admin/settings` | Read database-backed application settings |
| `PUT` | `/api/v1/admin/settings` | Validate, save, and apply application settings |

Login accepts exactly one JSON object:

```json
{
  "username": "admin",
  "password": "the configured password",
  "remember": true
}
```

`remember` is optional and defaults to `false`. The login dialog exposes it as
“Keep me signed in”; the Settings page controls its duration. The default is 30
days, and changes apply to newly created remembered sessions.

The request body is limited to 8 KiB and unknown fields are rejected. Invalid
usernames and passwords share one HTTP 401 response. Five failed attempts from
the same direct peer within five minutes block further attempts from that peer
until the window expires. At most four password checks run concurrently. The
bounded tracker never trusts forwarded addresses.

Successful login creates a 256-bit random token. Only its SHA-256 digest is
stored server-side. The browser receives the token in the
`kaven_media_session` cookie with `Path=/`, `HttpOnly`, and `SameSite=Strict`.
It is never exposed to JavaScript. Without `remember`, it is a browser-session
cookie and the in-memory session expires after 30 minutes without an
authenticated request or 12 hours after creation. It also ends when the server
process restarts.

With `remember`, the cookie receives an explicit expiry and the token digest is
stored in SQLite until that expiry, allowing the session to survive browser and
server restarts. Plaintext session tokens are never stored. Persistent sessions
are bound to the active credential verifier, so changing a configured password
invalidates earlier remembered sessions even though their rows may remain until
expiry cleanup. At most 128 sessions are retained in memory and in SQLite;
creating another evicts the oldest applicable session.

Logout is idempotent. It removes both the in-memory and remembered server-side
token and expires the cookie, so replaying the old cookie does not restore
access. Restarting the process invalidates ordinary sessions while remembered
sessions remain usable through their configured expiry. Rotating credentials
invalidates both kinds of session.

The Settings page can replace a password created during first-run setup. The
request requires the current password and a different valid new password. A
successful change atomically stores a new bcrypt verifier, revokes every active
session, and restarts the HTTP lifecycle before signing the current browser in
again. Passwords supplied through environment variables or a secret file remain
read-only in the UI and must be changed through the deployment configuration.

Unsafe authenticated requests require an `Origin` matching the request Host.
Cross-origin requests are rejected before mutation. Non-browser clients without
an Origin remain usable after obtaining a cookie from the login endpoint.

Passwords and session cookies require transport confidentiality. An
Internet-facing deployment must terminate HTTPS at a reverse proxy and prevent
clients from bypassing that proxy. Forwarded headers do not establish a trusted
origin, scheme, or client address.

## Authorization boundary

The first release has one administrator role with full administrative access;
it does not claim multi-user authorization.

- `GET /images`, maintenance `/api/*` routes, and restore require a session.
- `POST /images/upload` is public by default. Set it
  in Settings to require a session.
- HFS listing, reads, directory creation, and upload require a session unless a
  root explicitly permits public reads.
- `/`, `/healthz`, `/server/info`, and `/image/*` remain public, subject to the
  optional image referer policy.

The UI checks the session on startup. It shows HFS and Settings only while the
server confirms authentication. A rejected protected request clears the local
navigation state. Logout revokes the server session and returns protected pages
to Upload.

## Rotation and recovery

Setup-managed credentials can be rotated from Settings. Environment credentials
are loaded once during startup. To rotate one, replace
the mounted secret atomically and restart the single application container.
Runtime reload is deferred so credential replacement and complete session
invalidation happen together.

There is no recovery backdoor. If a setup credential is lost, stop the service,
provide `KAVEN_ADMIN_PASSWORD_FILE` and optionally `KAVEN_ADMIN_USERNAME`, then
restart it. The environment credential overrides the stored verifier without
erasing media or changing the SQLite credential.

## Standards and guidance

- [OWASP Session Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)
- [OWASP Authentication Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html)
- [OWASP Secrets Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Secrets_Management_Cheat_Sheet.html)
- [NIST session management guidance](https://pages.nist.gov/800-63-4/sp800-63b/session/)
- [Docker build check: SecretsUsedInArgOrEnv](https://docs.docker.com/reference/build-checks/secrets-used-in-arg-or-env/)
