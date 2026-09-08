# Kaven Media Server

A self-contained media and file server implemented in Go. The new server will
retain compatibility with Kaven Image Server while replacing MongoDB with an
embedded SQLite database.

## Current status

This repository is the greenfield foundation for the rewrite. It currently
provides the application lifecycle, embedded SQLite schema, persistent data
layout, health endpoint, server information endpoint, opt-in image upload,
image metadata lookup, original delivery, bounded libvips transformations, and
concurrency-safe derivative caching with asynchronous access records, plus the
secure administrative credential and Digest authentication foundation,
virtual HFS root configuration and listing, embedded Vue UI, and container
packaging. HFS directory listing and safe file rendering/download are also
implemented, along with protected directory creation and bounded atomic file
uploads and asynchronous completed-download recording. Bing metadata retrieval
with bounded retries and idempotent, atomic archive synchronization are
implemented, as is safe random Bing image delivery. Bing scheduling is
shutdown-aware and runs daily without overlapping invocations. Protected
manual synchronization aliases and the protected image collection route are
also available. Offline backup and verified restore support moving a complete
installation between hosts without an external database.

## Documentation

- [Architecture](docs/ARCHITECTURE.md)
- [Authentication design](docs/AUTHENTICATION.md)
- [Legacy compatibility](docs/COMPATIBILITY.md)
- [Development guide](docs/DEVELOPMENT.md)
- [Backup and restore](docs/BACKUP.md)
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
Plain Go builds show a development placeholder. See the
[UI build instructions](docs/DEVELOPMENT.md#ui-workflow) to embed the frontend
locally, or use Docker for the complete application.

## Run with Docker

```sh
docker compose up --build
```

All durable state is stored in the `kaven-media-data` volume mounted at
`/data`. No external database is required.
The Docker build compiles the frontend from `web/` and embeds it in the Go
executable; only one application process runs in the final container.
AMD64 and ARM64 build targets and runtime codec checks are configured; see
[container build instructions](docs/DEVELOPMENT.md#container-architectures).

## Data layout

```text
/data/
├── kaven-media.db
├── images/
├── cache/
├── bing/
├── hfs/
└── tmp/
```

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
Docker Hub secrets described in [release acceptance](docs/RELEASE.md#automated-publication)
before publishing the first release. The documented public snapshot publisher
keeps private development history out of GitHub while retaining one source
commit per release.
