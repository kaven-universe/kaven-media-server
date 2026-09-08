# Authentication and credential lifecycle

This document defines the implemented authentication contract for protected
routes. Credential loading, Digest middleware, replay protection, same-origin
authorization, and protected image-upload integration are implemented. Routes
that otherwise remain roadmap placeholders must not be described as available.

## First-run decision

A fresh zero-configuration instance starts with administration disabled. It
does not generate a password, print a secret to logs, persist a credential in
SQLite, or reuse the legacy `kaven` / `kaven2019` example credentials.

While administration is disabled:

- public health, server information, image lookup, and explicitly enabled
  public upload behavior remain available;
- dedicated administrative routes and private HFS roots are not mounted and
  return HTTP 404, avoiding an accidentally exposed or misleading
  authentication surface; the existing disabled upload route retains its
  documented HTTP 403 response;
- setting a custom admin username without a password is a configuration error
  and fails startup rather than silently disabling protection.

This chooses safe disablement over generated credentials because a generated
secret needs a secure delivery and recovery channel. Container logs and files
inside the persistent media volume are not acceptable secret-delivery channels.

## Configuration contract

The protected-route implementation supports one administrative identity:

| Setting | Default | Purpose |
| --- | --- | --- |
| `KAVEN_ADMIN_USERNAME` | `admin` | Digest authentication username |
| `KAVEN_ADMIN_PASSWORD_FILE` | unset | Absolute path to a mounted secret file; preferred |
| `KAVEN_ADMIN_PASSWORD` | unset | Direct environment fallback for development and constrained deployments |

There is intentionally no password command-line flag because process command
lines are commonly observable. The password-file and direct-password settings
are mutually exclusive; configuring both fails startup. Explicit but missing,
unreadable, non-regular, empty, or oversized password files also fail startup.

The secret file may end in one line ending, which is removed. Other leading or
trailing whitespace is part of the password. Passwords must be valid UTF-8,
contain no control characters, and be 16-1024 bytes after line-ending removal.
Usernames must be 1-64 ASCII characters from letters, digits, `.`, `_`, `@`,
and `-`. Secret values are never included in logs, errors, health responses, or
diagnostic output.

After validation, the authenticator computes the RFC 7616 SHA-256 `A1` value
from username, realm, and password and releases the original password string.
The derived value is still credential-equivalent for this realm and is treated
as a secret: memory only, never logged or persisted.

For containers, mount a secret outside `/data` and set only its path:

```yaml
services:
  kaven-media:
    environment:
      KAVEN_ADMIN_PASSWORD_FILE: /run/secrets/kaven_admin_password
    secrets:
      - kaven_admin_password

secrets:
  kaven_admin_password:
    file: ./secrets/kaven_admin_password.txt
```

Do not commit the referenced host file. Mounted secret files are preferred to
environment values because environment values can appear in inspection tools,
debug output, and process environments. Secrets must never be supplied as
Docker build arguments or baked into the image.

## HTTP authentication contract

Protected routes retain browser-driven HTTP Digest authentication for frontend
compatibility, with these deliberate security constraints:

- RFC 7616 `SHA-256` is the only accepted algorithm; MD5 and algorithm downgrade
  are not supported;
- the realm is `Kaven Media Server`, `qop=auth`, and UTF-8 is advertised;
- nonces contain an issue time and are authenticated with HMAC-SHA-256 using a
  random, process-local key; they expire after five minutes and all expire on
  restart or credential rotation;
- a bounded, expiring replay cache rejects reuse of the same username, nonce,
  client nonce, and nonce-count tuple while allowing distinct concurrent
  requests;
- usernames, realms, methods, and request targets are compared exactly, and
  response digests use constant-time comparison;
- malformed or invalid credentials receive HTTP 401 with a fresh challenge;
  an otherwise valid expired nonce adds `stale=true`.

Digest authentication does not provide transport confidentiality and does not
protect most headers or response bodies. Internet-facing deployments must put
the service behind TLS. The service must not infer a secure connection from
untrusted forwarded headers unless a future trusted-proxy configuration is
explicitly enabled.

Unsafe authenticated browser operations also require a same-origin check. An
`Origin` header, when present, must have the request's host; cross-site origins
are rejected before mutation. Non-browser clients without `Origin` remain
usable with valid Digest credentials.

## Authorization boundary

The first release has one `admin` role with full administrative access; it does
not claim multi-user authorization.

- `GET /images` and `/api/*` require admin authentication.
- `POST /images/upload` requires admin authentication unless
  `KAVEN_PUBLIC_UPLOADS=true` explicitly makes the legacy upload route public.
- HFS listing, reads, directory creation, and upload require admin access unless
  the selected root is explicitly declared public and the operation is a read.
- `/`, `/healthz`, `/server/info`, and `/image/*` remain public, subject to the
  optional image referer policy (`KAVEN_ALLOWED_DOMAIN_NAMES`). That filter does
  not authenticate callers and allows empty referers.

Authorization is evaluated before private path existence, directory contents,
or metadata are revealed.

## Rotation and recovery

Credentials are loaded once during startup. To rotate, replace the mounted
secret atomically and restart the single application container. The restart
invalidates all old nonces. Runtime reload is deferred because partial reloads
across credential and nonce state are harder to reason about safely.

There is no recovery backdoor. If a credential is lost, stop the service,
provide a new secret file, and restart it. Temporarily removing the credential
disables administrative routes; it does not make them public or erase media.

## Standards and guidance

- [RFC 7616: HTTP Digest Access Authentication](https://www.rfc-editor.org/rfc/rfc7616)
- [OWASP Secrets Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Secrets_Management_Cheat_Sheet.html)
- [Docker build check: SecretsUsedInArgOrEnv](https://docs.docker.com/reference/build-checks/secrets-used-in-arg-or-env/)
