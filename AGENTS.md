# AGENTS.md

## Mission

Build Kaven Media Server as a greenfield Go application that preserves the
useful public behavior of Kaven Image Server while providing a zero-config,
single-container installation. SQLite and files under one persistent data
directory replace the external MongoDB requirement.

This is a behavioral rewrite, not a line-by-line TypeScript port.

## Read first

Before implementing a feature, read these documents:

1. `docs/ARCHITECTURE.md`
2. `docs/COMPATIBILITY.md`
3. `docs/ROADMAP.md`
4. `docs/DEVELOPMENT.md` for build and verification commands

## Reference repositories

When this repository is checked out beside the legacy projects, use these as
read-only behavioral references:

- `../kaven-image-server`: Express/TypeScript backend
- `../kaven-image`: Quasar/Vue frontend

Do not modify either legacy repository unless the user explicitly asks. Do not
copy legacy implementation patterns automatically; reproduce externally useful
behavior with idiomatic Go and explicit tests.

## Current foundation

- Entry point: `cmd/kaven-media/main.go`
- Application lifecycle and routing: `internal/app/app.go`
- Configuration: `internal/config/config.go`
- SQLite initialization: `internal/database/database.go`
- Initial schema: `internal/database/schema.sql`
- Embedded UI hook: `internal/webui`
- Container interface: port `5558`, persistent volume `/data`

The public CLI supports `serve`, `check`, `backup`, `restore`, and `version`.
Legacy deployment conversion is not part of the public application.

## Product invariants

- A fresh Docker installation must start without an external database or a
  required configuration file.
- All durable state belongs under one configurable data directory, `/data` in
  the container.
- Restored image URLs based on ObjectId, UUID, or SHA-1 must remain usable.
- Existing frontend request and response shapes stay compatible until an
  intentional API version replaces them.
- The default deployment model is one application process using a local Docker
  volume. Do not imply that SQLite supports arbitrary multi-replica deployment.

## Implementation rules

- Prefer the Go standard library. Add a dependency only when it materially
  reduces risk or implements specialized functionality such as SQLite or
  libvips bindings.
- Keep HTTP handlers thin. Put storage, image processing, authentication, and
  scheduling logic behind focused packages.
- Use `context.Context` for request-scoped and shutdown-aware work.
- Wrap errors with operation context. Do not log and return the same error at
  every layer; log at process or request boundaries.
- Use explicit SQL and small repository types rather than introducing an ORM.
- Store timestamps in UTC using a single documented representation.
- Preserve legacy IDs as text. New IDs should have a stable, documented format.
- Apply schema changes through numbered, embedded migrations. Do not keep
  expanding one unversioned bootstrap script once development proceeds.
- Write uploaded files to a temporary file, validate and checksum them, then
  atomically rename them into place. Clean up temporary files on every failure.
- Treat filenames, URL path segments, MIME types, forwarded IP headers, and
  configuration values as untrusted input.
- Resolve and verify filesystem paths remain inside their configured root before
  opening, writing, renaming, or deleting files.
- Enforce request-body, multipart-file, image-dimension, and concurrency limits.
- Do not restore the legacy hard-coded default password. A fresh instance must
  either generate credentials, require an explicit secret for protected
  features, or keep those features safely disabled until configured.

## API compatibility workflow

For each legacy endpoint:

1. Capture representative requests and responses from the old server.
2. Add contract tests or fixtures in this repository.
3. Implement the new behavior.
4. Compare status, headers, JSON casing, error codes, and file bytes or metadata.
5. Document any deliberate incompatibility in `docs/COMPATIBILITY.md`.

Preserve behavior needed by the frontend and existing URLs. Do not preserve a
security flaw merely for compatibility.

## Testing expectations

- Use table-driven unit tests for parsing, validation, path containment, and
  response mapping.
- Use `httptest` for HTTP behavior.
- Use `t.TempDir()` and a real temporary SQLite database for repository and
  integration tests.
- Test malformed and adversarial inputs, not only successful requests.
- Run `go test ./...` after every coherent change.
- Run `go test -race ./...` for changes involving goroutines, schedules,
  caches, database access, or shutdown.
- Run `go vet ./...` before declaring a feature complete.

## Definition of done

A feature is complete only when its implementation, tests, error behavior,
configuration, and relevant documentation are all updated. For compatibility
features, it must also be checked against the legacy behavior or have a
documented intentional difference.
