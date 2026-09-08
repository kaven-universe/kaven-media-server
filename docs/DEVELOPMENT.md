# Development

## Requirements

- Go 1.25 or newer
- Docker with Compose for container verification
- libvips 8.14+, a C compiler, and `pkg-config` for native transformation builds
- Optional sibling checkouts of `kaven-image-server` and `kaven-image` for
  compatibility work

## Common commands

```sh
go mod tidy
gofmt -w ./cmd ./internal
go test ./...
go test -race ./...
go vet ./...
go run ./cmd/kaven-media serve --data-dir ./data
go run ./cmd/kaven-media check --data-dir ./data
go run ./cmd/kaven-media check --data-dir ./data --json
go run ./cmd/kaven-media verify-ci --evidence-dir ./ci-evidence --revision 0123456789abcdef0123456789abcdef01234567
go run ./cmd/kaven-media backup --data-dir ./data --output ./backups/snapshot
go run ./cmd/kaven-media restore --input ./backups/snapshot --data-dir ./restored-data
go run ./cmd/kaven-media version
docker compose up --build
```

The production image builds with `CGO_ENABLED=1` and the `libvips` build tag.
To exercise the same path locally, install the libvips development package and
run `go test -tags=libvips ./internal/imageproc` or
`go build -tags=libvips ./cmd/kaven-media`. A build without that tag retains
original image delivery but returns HTTP 501 for transformation requests. The
container build runs the JPEG, PNG, WebP, GIF, TIFF, AVIF, HEIF, and SVG codec
matrix before producing the executable, then runs its compiled test binary
against the final runtime libraries as the unprivileged container user. Codec
tests run in a dedicated process because govips cannot restart after shutdown;
embedded UI and application tests run separately with the `webui` tag. The
matrix reopens transformed bytes, checks their dimensions and format, and
forces pixel decoding. The test binary is mounted only during the build and
is absent from the resulting image.

## Container architectures

`docker-bake.json` defines `linux/amd64` and `linux/arm64` build targets. The
frontend runs on the builder's architecture because its output is static;
Go, CGO, libvips, and the codec tests run on the target architecture. Both Go
and runtime stages use Alpine 3.22 to keep their native libraries aligned.
Both explicitly install `vips-heif`: Alpine packages this loader separately
from base `vips`, and its `libheif` dependency includes the AV1 and HEVC codecs.
See the [Alpine libheif package](https://pkgs.alpinelinux.org/package/v3.22/community/aarch64/libheif).

Build and load one architecture for local execution:

```sh
docker buildx bake --set image.platform=linux/amd64 --load
docker run --rm -p 5558:5558 -v kaven-media-data:/data kaven-media-server:local
```

Release builds should set a stable version and full source revision. These
values are embedded in `kaven-media version` and the OCI image labels:

```sh
docker buildx bake \
  --set image.platform=linux/amd64 \
  --set image.args.VERSION=0.9.0 \
  --set image.args.REVISION=0123456789abcdef0123456789abcdef01234567 \
  --load
docker run --rm kaven-media-server:local version
```

Use a simple release identifier and hexadecimal revision without spaces. The
defaults are `dev` and `unknown`. Native Go builds normally report version and
VCS information recorded by the Go tool; `-trimpath` does not remove it. Build
scripts can override both values with linker `-X` flags targeting
`internal/buildinfo.Version` and `internal/buildinfo.Revision`.

Use `linux/arm64` on an ARM host. To export both architectures without pushing
to a registry, use a Buildx builder with native workers or QEMU support for
both platforms:

```sh
docker buildx create --name kaven-builder --driver docker-container --use
docker buildx inspect --bootstrap
mkdir -p bin
docker buildx bake --set image.output=type=oci,dest=bin/kaven-media-server.oci.tar
```

The builder must support executing each platform, including its C compiler and
tests. Native workers avoid the cost of emulated compilation. The OCI archive
contains both platforms; use the single-platform command above for ordinary
local Docker loading. See [Docker multi-platform builds](https://docs.docker.com/build/building/multi-platform/).

The `check` command prints a deterministic consistency report and exits with an
error when it finds SQLite integrity or foreign-key failures, unsafe or missing
database file references, invalid cache rows, missing managed directories, or
orphaned files under `images`, `cache`, or `bing`. It streams every referenced
original image up to the 100 MiB image limit and compares its byte count and
SHA-1 with the stored record; SHA-1 is retained here solely for
legacy-compatible integrity verification.
HFS content is user-managed and is not classified as orphaned. Pass `--json`
for automation. Both formats retain at most 1,000 issue details while reporting
the full `issueCount` and `truncatedIssues`; finding any issue still produces a
nonzero exit. CLI JSON also includes `checksumsChecked`, `checksumBytes`, the
ordered applied `schemaVersions`, destination `tableCounts`, and an `execution`
object with UTC generation time and build identity. These versions and counts
reflect the database after `database.Open` validates history and applies
pending embedded migrations.

Backup and check require the server to be stopped. They share an OS lock with
`serve`. Backup and
restore support Linux and Windows, require an existing destination parent and
an absent destination, and default to a 1 TiB total-file limit (`--max-bytes`).
Both operations require the inventoried file bytes plus a 64 MiB reserve to be
available on the destination filesystem before creating staging content.
See [BACKUP.md](BACKUP.md) for format, exclusions, validation, Docker commands,
and interrupted-operation recovery. Tests cover committed WAL recovery,
unchanged source bytes, checksum failures, malformed paths/manifests, process
lock release, interrupted-copy cleanup, and restoration into a fresh database.

## Continuous integration

GitHub Actions runs formatting validation, the unit and integration tests, the
race detector, `go vet`, and production Docker image builds for pull requests
and release tags. The workflow can also be started manually with
`workflow_dispatch`. Public snapshot branch updates accompany release tags, so
ordinary branch-push CI is disabled to avoid running the same acceptance suite
twice for one release.

The container matrix uses native `ubuntu-24.04` (AMD64) and
`ubuntu-24.04-arm` (ARM64) runners. Each builds and loads its Bake target,
verifies image architecture, and starts the server to check health, embedded
UI assets, and missing/private routes. The Dockerfile gates both builds on
the runtime codec matrix. CI embeds the full commit revision in the executable
and OCI label, then verifies both. CI does not publish images. Container and native
codec execution still require a successful Docker/CI run; plain Go tests
without the `libvips` tag cannot establish codec support.

The race detector runs on Linux with CGO enabled. Local Windows installations
without a C compiler can still run the remaining checks from the command list
above.

After downloading and extracting the combined CI artifact, `verify-ci` checks
its four SHA-256 entries, bounded regular JSON files, manifest identity, exact
candidate revision, quality gates, and native AMD64 and ARM64 image records.
It also verifies image platform, embedded revision, unmodified build identity,
runtime-codec result, and HTTP smoke result. Retain its JSON output with release
evidence; it identifies the accepted run URL and attempt. The assembly job runs
the same command before upload and includes its `verification.json` output.

On PowerShell, `gofmt` accepts file paths rather than package patterns. Use a
tracked-file list or an editor integration if `gofmt -w ./cmd ./internal` is not
accepted by the installed tool.

## Local endpoints

- UI: <http://localhost:5558/> (development placeholder without the `webui` tag)
- Health check: <http://localhost:5558/healthz>
- Server information: <http://localhost:5558/server/info>
- Image collection: <http://localhost:5558/images> (requires configured admin
  credentials; returns at most 100 records, newest creation time first)

## Configuration policy

Every option has a safe default where possible. Environment variables use the
`KAVEN_` prefix. CLI flags override environment variables. A future config file
is optional and must not be required for a standard Docker start.

Currently implemented:

| Environment variable | Flag | Default |
| --- | --- | --- |
| `KAVEN_LISTEN` | `--listen` | `:5558` |
| `KAVEN_DATA_DIR` | `--data-dir` | `./data`, `/data` in Docker |
| `KAVEN_PUBLIC_UPLOADS` | `--public-uploads` | `false` |
| `KAVEN_ALLOWED_DOMAIN_NAMES` | none | `[]` (image referer filtering disabled) |
| `KAVEN_ADMIN_USERNAME` | none | `admin` |
| `KAVEN_ADMIN_PASSWORD_FILE` | none | unset |
| `KAVEN_ADMIN_PASSWORD` | none | unset |
| `KAVEN_HFS_ROOTS` | none | `[{"name":"uploaded","path":"hfs/uploaded","public":false}]` |

Public uploads are deliberately opt-in. For a compatibility deployment that
needs the legacy anonymous image uploader, set `KAVEN_PUBLIC_UPLOADS=true` on
the container after considering network-level access controls.

`KAVEN_ALLOWED_DOMAIN_NAMES='["example.com","media.example.net"]'` enables
referer filtering on original/derived image and metadata routes and Bing random
images, including HEAD requests. Specify up to 64 DNS names or IP literals in
a JSON array of at most 16 KiB. Names are case-insensitive; schemes, ports,
wildcards, paths, trailing dots, and raw Unicode names are rejected (use ASCII
punycode for international names). DNS names allow their subdomains; IP literals
match exactly. Include the UI's public hostname when enabling this setting.

For legacy compatibility, empty referers, localhost, loopback IPs, and private
IPv4/IPv6 addresses are allowed. No DNS lookup is performed. Malformed,
duplicate, non-HTTP(S), credential-bearing, fragmented, or over-8-KiB referers
receive HTTP 403. Enabled responses vary on Referer, and denials use `no-store`.
Unset or `[]` disables filtering, including malformed-header checks. This is
hotlink filtering, not authentication: callers can omit or forge Referer.
Migration does not automatically apply the legacy domain list; configure it
explicitly. No author-specific domains are built into a fresh installation.

When adding a setting, document its default, units, security impact, and Docker
example. Secrets must support Docker secrets or file-based input; do not require
placing passwords directly in command history.

The protected-route credential and authorization contract is in
[AUTHENTICATION.md](AUTHENTICATION.md). The password-file setting is preferred;
it and the direct-password fallback are mutually exclusive. There is
intentionally no password CLI flag. Administrative and private HFS routes that
are not implemented remain unavailable rather than exposing placeholders.

`KAVEN_HFS_ROOTS` is an ordered JSON array with at most 64 entries. Names use
letters, digits, `.`, `_`, `@`, and `-`; paths use forward slashes and must be
non-overlapping descendants of `hfs/`. The read-only external-root exception is an
absolute path paired with `"readOnly":true`. For example, a Compose service can
add a public managed root while retaining a private root:

```yaml
environment:
  KAVEN_HFS_ROOTS: >-
    [{"name":"shared","path":"hfs/shared","public":true},{"name":"private","path":"hfs/private"}]
```

The application creates configured managed root directories at startup. Public
means unauthenticated reads; managed HFS writes remain protected. Absolute
read-only roots must already exist and expose no write route.

HFS directory responses contain at most 10,000 regular files and directories.
Path segments containing traversal, separators, drive/stream syntax, control
characters, or invalid UTF-8 are rejected, and symlinks are neither listed nor
opened. Supported image, audio, video, PDF, and configured legacy text types
render inline with restrictive response headers; everything else downloads as
an attachment.

HFS uploads use multipart field `file`, accept at most 100 files, and cap each
file at 1,048,576,000 bytes. The aggregate request is also bounded. Uploads are
staged in `tmp` and published with no-overwrite semantics; a failed batch rolls
back its successfully published files. At most two HFS mutations run
concurrently. `?mkdir` creates only the final path component and requires its
parent directory to exist.

The HFS acceptance suite includes encoded traversal at the pre-routing guard,
symlinked read and mutation targets, concurrent same-name publication,
pre-existing-file preservation, batch rollback, and interrupted multipart
cleanup. Symlink cases skip only when the host cannot create test symlinks.

## Database development

Database opening validates recorded migration history before enabling WAL or
applying pending migrations. Newer/unknown versions, duplicate versions, missing
earlier versions, and unreadable history fail startup with an error. A fresh
database or a valid prefix of this build's migrations can advance normally.
Tests verify rejected opens preserve database bytes and create no journals.
This guards `serve` and `check` through their shared
database opener; backup/restore retain their stricter exact-version validation.

Tests should open SQLite in `t.TempDir()` rather than reusing developer data.
Avoid SQLite `:memory:` for tests that use multiple pooled connections unless
the DSN and pool behavior are intentionally configured for a shared in-memory
database.

The live database uses WAL. Tests that assert created files must account for
`kaven-media.db-wal` and `kaven-media.db-shm`.

Repository timestamps are stored as UTC text with exactly three fractional
digits (`2006-01-02T15:04:05.000Z`). Repository reads also accept other valid
RFC 3339 fractional precision.

Storage paths passed to `internal/storage` are relative and platform-neutral.
The storage layer treats both slash styles as separators, rejects traversal,
absolute paths, drive or alternate-stream syntax, and symlinked descendants,
and never replaces an existing file during atomic publication. Writes are
staged under `tmp`, synchronized, optionally validated, then published.
Read-only external HFS roots accept an absolute existing path only after
checking every component for symlinks.

New image IDs and UUIDs are the same lowercase, 32-character UUIDv4 value.
Client-provided UUIDs may contain hyphens but are normalized and must decode to
exactly 16 bytes. Stored image filenames use this UUID plus the extension
derived from file signatures; client filenames never determine a storage path.

Derived images use a SHA-256 filename calculated from their canonical cache
key and are stored below `cache/<first-two-digest-characters>/`. Cache database
rows are written only after an atomic file publication. A failed database write
removes the newly published file, while the `check` command reports orphaned
files left by process interruption.

Image access records pass through an in-memory queue of 256 events and one
SQLite writer. Enqueue is non-blocking; a full queue drops new audit events so
image delivery remains independent of database latency. Writes time out after
two seconds, and graceful shutdown allows five seconds to drain accepted
events. Only the direct socket peer is recorded; forwarded-IP headers are not
trusted without a future explicit proxy configuration.

HFS download records use a separate queue with the same 256-event capacity,
two-second SQLite write deadline, five-second shutdown drain, and direct-peer
IP policy. File paths and request targets are limited to 8 KiB, IP strings to
128 bytes, and user agents to 1 KiB. Enqueue occurs only after a complete
attachment response and remains non-blocking.

The Bing metadata client defaults to
`https://cn.bing.com/HPImageArchive.aspx`. It permits 1-100 images per fetch,
uses a ten-second per-attempt timeout, and makes at most three attempts with a
250 ms exponential initial delay capped at five seconds. Metadata bodies are
limited to 2 MiB. Tests inject an `httptest` server and never require Bing
network access.

Bing archive downloads use the same HTTPS origin by default, remove `w` and
`h` query parameters, time out after 30 seconds, and are limited to 50 MiB.
Downloaded bytes are staged, signature-validated, and published at a stable
URL-derived path below `bing/<year>/`; malformed dates use `bing/unknown/`.
Synchronization is safe to rerun and serializes concurrent calls. The server
runs it every 24 hours, beginning after the first interval rather than during
startup. Scheduled invocations never overlap. Shutdown cancels active metadata
requests, retry waits, and downloads and waits for the job worker to return.

`GET /image/bing/random` serves a randomly selected downloaded archive entry.
It retries stale missing-file references up to 20 times, then returns 404. The
handler supports `HEAD`, byte ranges, and modification-time validators and
validates both storage containment and the actual image signature before
serving bytes.

With administration enabled, both legacy manual aliases enqueue the same
canonical archive synchronization and return an empty HTTP 202 response:

- `/api/sync-bing-images-from-db`
- `/api/sync-bing-images-from-dir`

The aliases are unavailable when administrative credentials are disabled.
Only one pending manual run is retained while synchronization is active.

## UI workflow

Docker automates the production flow:

1. Build the owned Quasar frontend in `web/` with `pnpm build` (after
   `pnpm install --frozen-lockfile`; use Node.js 24 and pnpm 11.8.0).
2. Copy its compiled output into `internal/webui/dist` before `go build`.
3. Run native codec tests separately from embedded UI and application tests,
   then build Go with `libvips,webui`.
4. Serve API and UI from the same origin.

The build output is `web/dist/spa`, and API requests use the page's origin.
Node and pnpm are absent from the runtime image. CI starts the image and checks
the index, JavaScript entry point, health endpoint, and missing/private routes.
After quality and both native container jobs pass, CI combines their structured
JSON results, build identity, image IDs, workflow coordinates, and checksums in
a `ci-evidence-<commit>` artifact retained for 90 days.

For a local build with the UI, after building the frontend, run from the root:

```sh
mkdir -p internal/webui/dist
cp -R web/dist/spa/. internal/webui/dist/
go test -tags=webui ./...
go build -tags=webui -o bin/kaven-media ./cmd/kaven-media
```

On PowerShell, replace the directory and copy commands with:

```powershell
New-Item -ItemType Directory -Path internal/webui/dist -Force
Copy-Item -Path web/dist/spa/* -Destination internal/webui/dist -Recurse -Force
```

Use a clean generated `internal/webui/dist` directory for release builds so
obsolete chunks from earlier builds are not included. Docker always uses its
fresh frontend-stage output. Add `libvips` to the tags when the native build
requirements are installed. Plain `go test ./...` and `go run` require no Node
installation and embed the development fallback instead. The `webui` tag fails
to compile when generated output is missing.

See [`web/README.md`](../web/README.md) for development proxy, type-checking,
linting, and source provenance. Do not commit `node_modules`, generated assets,
or temporary frontend build caches.

## Commit discipline

Keep changes small enough to verify. A compatibility endpoint should normally
arrive with its contract tests and documentation update in the same commit.
Avoid mixing mechanical restructuring with behavioral changes.
