# Kaven Media Server

A self-contained media and file server implemented in Go with an embedded
SQLite database and filesystem-backed media storage.

## Current status

This repository is a greenfield implementation. It currently
provides the application lifecycle, embedded SQLite schema, persistent data
layout, health endpoint, server information endpoint, public image upload,
image metadata lookup, original delivery, bounded libvips transformations, and
concurrency-safe derivative caching with asynchronous access records, plus the
secure administrative credential and server-side session authentication,
virtual HFS root configuration and listing, embedded Vue UI, and container
packaging. HFS directory listing and safe file rendering/download are also
implemented, along with protected directory creation and bounded atomic file
uploads and asynchronous completed-download recording. Bing metadata retrieval
with bounded retries and idempotent, atomic archive synchronization are
implemented, as is safe random Bing image delivery. Configurable Bing
scheduling is shutdown-aware and defaults to daily without overlapping
invocations. Protected manual synchronization controls and the protected image collection route are
also available. Offline database backup and verified restore support moving
SQLite state between hosts; large managed directories are transferred or
mounted separately.

## Documentation

- [Architecture](docs/ARCHITECTURE.md)
- [Authentication design](docs/AUTHENTICATION.md)
- [Development guide](docs/DEVELOPMENT.md)
- [Backup and restore](docs/BACKUP.md)
- [Database import](docs/IMPORT.md)
- [Upgrade and rollback](docs/UPGRADE.md)
- [Security](SECURITY.md)
- [Release acceptance](docs/RELEASE.md)
- [Roadmap](docs/ROADMAP.md)
- [Agent instructions](AGENTS.md)

## Run locally

```sh
go run ./cmd/kaven-media serve --data-dir ./data
```

Open <http://localhost:5558> or check <http://localhost:5558/healthz>.
On the first Docker/UI start, the setup page asks you to create the administrator
account before opening the normal interface.
Plain Go builds show a development placeholder. See the
[UI build instructions](docs/DEVELOPMENT.md#ui-workflow) to embed the frontend
locally, or use Docker for the complete application.

## Run with Docker

```sh
docker compose up --build
```

All durable state is stored in the host directory `./data`, bind-mounted at
`/data`. Set `KAVEN_DATA_PATH` before starting Compose to use another absolute
host path. The directory must be writable by the container's `kaven` user.
Because this is an ordinary host directory, trusted local programs can access
selected files directly. Stop every writer before backup, restore, or upgrade,
and do not modify the SQLite database while the server is running. No external
database is required.
The administrator created by the first-run page is stored as a password verifier
inside SQLite, so it persists with the bind-mounted data directory.
The Docker build compiles the frontend from `web/` and embeds it in the Go
executable; only one application process runs in the final container.
AMD64 and ARM64 build targets and runtime codec checks are configured; see
[container build instructions](docs/DEVELOPMENT.md#container-architectures).

In a container management UI, add a **bind** mount with the chosen host data
directory as its source and `/data` as its read/write container target. Do not
select a managed or anonymous volume. Keep the same host source path when the
container is recreated or its image is upgraded.

## Data layout

The upload and download roots are database-backed settings. Their defaults are
shown below; both are canonical paths relative to `/data`.

```text
/data/
├── kaven-media.db
├── logs/
│   ├── kaven-media.log
│   └── kaven-media-20260916T133012.123456789Z.log
├── upload/
├── cache/
├── download/
│   └── bing/
├── tmp/
└── <operator directories>/
```

The server writes structured text records to the container console and, by
default, `/data/logs/kaven-media.log`. Settings can disable the local file and
change its 10 MiB rotation size and five-file retention limit (zero retains all
rotated files without automatic deletion); console output
always remains enabled. Logs survive container replacement through the existing
`/data` bind mount. The private `logs` directory cannot be exposed through HFS
or used as an upload/download root.

## Commands

```text
kaven-media serve
kaven-media check [--json]
kaven-media backup --output PATH
kaven-media restore --input PATH --data-dir NEW_PATH
kaven-media version
```

`serve`, `check`, offline `backup`, and verified `restore` are implemented.
Stop the server before check or backup; they share an exclusive data-directory
lock. Restore publishes only to a new path.
`version` prints machine-readable build identity without opening the data
directory.

## Releases

Semantic version tags run the full release acceptance workflow, publish the
AMD64/ARM64 image to Docker Hub with `latest` and an automatically detected
major-version tag such as `1`, and create a GitHub Release with checksummed
Alpine Linux binaries and CI evidence. Repository maintainers must configure the
registry credentials described in [release acceptance](docs/RELEASE.md#automated-publication)
before publishing the first release. The documented public snapshot publisher
keeps private development history out of GitHub while retaining one source
commit per release.
