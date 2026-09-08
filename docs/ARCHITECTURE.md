# Architecture

## Goals

Kaven Media Server is a single-process Go service for image hosting, image
transformation, Bing wallpaper archiving, and lightweight file hosting. It is
designed to run from one Docker image with one persistent volume and no external
database.

Primary goals:

- zero-config first start;
- compatibility with useful Kaven Image Server URLs and frontend behavior;
- durable, recoverable local storage;
- bounded resource use for untrusted uploads and image transformations;
- a structure that remains understandable without framework-specific magic.

Non-goals for the first stable release:

- active/active replicas sharing one SQLite file;
- storing image binaries inside SQLite;
- exact reproduction of internal Mongoose models or Express middleware;
- redesigning the existing Vue UI during backend parity work.

## Runtime view

```text
Browser / API client
        |
        v
Go HTTP server ---- embedded Vue assets
        |
        +---- handlers ---- application services
                              |       |
                              |       +---- image processor (libvips)
                              |
                              +---- repositories ---- SQLite
                              |
                              +---- file store ------ /data
                              |
                              +---- scheduler ------- Bing downloader
```

SQLite stores metadata and audit records. Original images, derived images, Bing
downloads, and HFS content remain ordinary files. Database rows should store
paths relative to the data directory wherever possible so a Docker volume can
be moved or restored at a different host path.

## Persistent layout

```text
/data/
├── kaven-media.db
├── images/
├── cache/
├── bing/
├── hfs/
└── tmp/
```

`tmp` contains incomplete uploads and transformations and may be cleaned at
startup according to a conservative age threshold. A database file and its
`-wal` and `-shm` files must stay on the same local filesystem.

## Package direction

The current scaffold is intentionally small. Grow it toward these boundaries:

```text
cmd/kaven-media          CLI parsing and process exit
internal/app             composition, lifecycle, HTTP server
internal/config          defaults and environment/config parsing
internal/httpapi         routes, middleware, JSON contracts
internal/auth            authentication and authorization
internal/database        SQLite connection and schema migrations
internal/repository      SQL persistence implementations
internal/storage         safe paths and atomic filesystem operations
internal/imageproc       metadata, resize, encoding, resource limits
internal/bing            Bing client and archive synchronization
internal/scheduler       background job lifecycle
internal/webui           embedded compiled frontend
```

Dependencies should point inward toward domain types and interfaces. Repository
and image-processing implementations must not leak driver-specific types into
HTTP contracts.

Administrative authentication is disabled until an explicit secret is
configured. The credential, Digest challenge, nonce, replay, TLS, rotation, and
route-authorization contract is defined in [AUTHENTICATION.md](AUTHENTICATION.md).

## Database

Use SQLite through `database/sql`. The connection setup enables foreign keys,
WAL, and a busy timeout. Keep transactions short and avoid holding a transaction
open while downloading, hashing, or transforming files.

Numbered embedded migrations in `internal/database/migrations` are recorded in
`schema_migrations`. Each pending migration executes in a transaction when the
database opens, including from `check`. Before journal configuration or schema
writes, recorded versions must be an ordered prefix of the embedded migration
versions; unknown or duplicate versions, gaps in recorded history, and malformed
versions are rejected. A database without a migration table can still initialize.
There are no down migrations; rollback
uses the previous build and its pre-upgrade data as described in
[UPGRADE.md](UPGRADE.md).

Image IDs are stored as `TEXT`, which preserves restored ObjectIds and stable
`/image/:id` URLs without an ID translation table. New uploads use UUID-based
text IDs. Read-only HFS roots may point outside the data directory; every path
component is checked for symlinks and mutation routes remain disabled.

## Files and consistency

For new uploads:

1. Stream into `/data/tmp` with a hard byte limit.
2. Inspect magic bytes; do not trust the supplied content type or extension.
3. Decode metadata with dimension and pixel-count limits.
4. Calculate the compatibility SHA-1 and a modern internal digest if adopted.
5. Move the file atomically into its final location.
6. Insert the database row in a short transaction.

If the database insert fails after the rename, remove only the newly created
file whose ownership is known. The `check` command must detect orphaned files,
missing files, invalid cache entries, and broken foreign-key relationships.

## Image processing

Sharp in the legacy service uses libvips. A Go libvips binding is therefore the
best route to format and performance parity. The Docker build must explicitly
package the required runtime codecs and test JPEG, PNG, WebP, GIF, TIFF, AVIF,
HEIF, and SVG behavior rather than assuming the library build supports them.

Cache keys must be canonical. Parse and validate width, height, quality, and
output format before constructing a cache key. Deduplicate concurrent requests
for the same missing derivative so only one transformation is performed.

Image access audit writes run through a bounded single-worker queue. Delivery
must not wait for SQLite or fail because an audit insert failed. Graceful
shutdown drains accepted events within a fixed deadline; saturation may drop
audit events rather than applying unbounded memory growth or request backpressure.

## Bing archive

The Bing metadata boundary is a small standard-library HTTP client. It calls
the legacy homepage archive resource over HTTPS, applies a deadline to every
attempt, limits response bytes and decoded collection sizes, and preserves the
source field casing in an internal DTO. Only transport failures and transient
HTTP statuses are retried. Retry count and exponential delay are bounded,
server `Retry-After` values are capped, and caller cancellation interrupts both
requests and backoff. Download, persistence, and scheduling remain separate
services so no database transaction spans a network operation.

Archive synchronization is serialized per service instance and remains
caller-cancellable while waiting. Each metadata URL is reconciled with SQLite:
a valid referenced file is reused, a missing reference is fetched again, and a
valid deterministic orphan left by an interrupted run is adopted. Unsafe or
invalid existing references fail without replacement. New images are
downloaded with a fixed deadline and byte limit, staged under `tmp`, validated
from their signature, and atomically published under `bing/<year>/`. The SQLite
upsert happens only after publication; a failed upsert removes only a file
created by that attempt. Per-image failures are collected while independent
images continue.

Random delivery preserves the legacy 20-selection bound for stale database
references. Each candidate path must remain below `bing/`; symlinks and
non-regular files are rejected, and the opened handle is rechecked against the
validated path before signature detection. Successful responses use the
detected MIME type and standard `ServeContent` range and modification-time
semantics.

The process scheduler owns one serial worker loop per named job. Jobs wait for
their first interval rather than delaying startup, and a job cannot overlap its
previous invocation. Shutdown cancellation is passed into active work, all job
workers return before repositories and SQLite are closed, and failures are
logged without stopping future intervals. Bing archive synchronization runs
once every 24 hours through this lifecycle.

The authenticated legacy maintenance aliases feed the same named scheduler
job through a capacity-one trigger. A request starts the job promptly when it
is idle, keeps at most one run pending while it is active, and coalesces excess
requests. This bounds memory and goroutine use while retaining the legacy
asynchronous 202 response.

## HFS roots

Virtual HFS roots map URL-safe names to non-overlapping relative directories
below `hfs/` in the persistent data directory. The default is a private
`uploaded` root at `hfs/uploaded`. Absolute host paths and paths into other
managed areas are rejected so one-volume backup and containment guarantees stay
intact. An explicit external root may use an existing absolute path only
with `readOnly: true`; it is non-portable and cannot be mutated through the
server. A public root permits unauthenticated reads only. Managed-root mutations
always require the administrative identity.

HFS URL segments are decoded exactly once and validated before route
normalization or filesystem access. Opens reject symlinks and verify that the
opened handle still identifies the validated path. Directory listings include
only regular files and directories, omit unsafe names and special entries, and
are capped at 10,000 entries. File responses use signature-based media
detection for supported images and a small legacy text-extension allowlist;
other content is downloaded as an attachment.

HFS mutations are always administrative, including on public roots. Directory
creation adds exactly one missing directory below an existing directory and
never treats a client path as an instruction for recursive creation. Multipart
files are staged under `tmp`, bounded individually and by count/request size,
then published without replacement. A failed multi-file publication rolls back
only files created by that request; staged files are removed on every exit.
At most two HFS mutations stage or publish concurrently across all roots.

Completed attachment responses enqueue download audit events after all expected
response bytes have been written. Inline views, HEAD and validator responses,
and failed or incomplete transfers are excluded. A bounded single-worker queue
keeps SQLite latency and failure out of the response path; accepted events are
drained within a fixed graceful-shutdown deadline, while saturation may drop
events. Records store only the configured relative file path, original request
target, direct peer IP, optional user agent, and UTC completion time.

## HTTP and UI

An optional bounded domain allowlist filters Referer on image and Bing routes
before lookup or transformation. Empty referers and local/private IP literals
retain legacy allowances; malformed headers fail closed when enabled. Filtering
is disabled by default and is not authentication. Enabled responses vary on
Referer to keep shared caches from mixing decisions between referring sites.

Use `net/http` routing and middleware. Compatibility handlers retain legacy
paths and JSON field casing. New administrative or diagnostic APIs should be
versioned rather than silently changing legacy responses.

The compiled Quasar/Vue UI can be embedded with `go:embed`, giving the Docker
image one server and one origin. During development, the UI may run separately
with a proxy to the Go server. The application now owns its frontend source in
`web/`, including the shared compatibility types, so builds need no sibling
checkout. API requests use the page's origin and the router uses hash URLs.
Docker builds the SPA in a separate Node stage with frozen pnpm dependencies,
then copies it into the Go build stage. The `webui` build tag embeds the complete
generated tree, including filenames beginning with underscores. Missing output
fails compilation, and production tests require a branded module-based index
and all linked assets. The runtime image contains no Node service or frontend
source dependency. Plain Go builds use a separate development placeholder.

Static responses support GET and HEAD, disable directory listings, and require
cache revalidation. Hash routing needs no catch-all HTML response: missing
assets and unavailable API routes stay 404 rather than receiving the SPA.

The administrative restore page uploads the ordinary backup directory as
bounded multipart files whose field names carry validated snapshot-relative
paths. The server reconstructs the snapshot under `tmp`, invokes the same
checksum, producer, schema, and integrity validation as CLI restore, and records
a completed candidate before acknowledging the request. It then shuts down its
HTTP and background resources, moves the active managed entries into a private
rollback directory, moves the candidate into place, and starts again in the
same container. Small durable phase files make each move resumable after an
abrupt process or host restart. The route is absent unless administration is
configured.

## Deployment boundary

Container build targets cover Linux AMD64 and ARM64. The frontend stage uses
the builder architecture; the Go/CGO build and native tests use the target
architecture. Build and runtime stages share an Alpine release and explicitly
include the HEIF loader. Before an image can finish building, its runtime
libraries must pass the codec matrix under the runtime user. Native CI runners
also check the built architecture and exercise the running HTTP server.

The supported default is one container and one local Docker volume. Backups
must include both SQLite and files from a mutually consistent point in time.
Offline backup holds the same OS-level data-directory lock as the server,
importer, and checker. It copies SQLite and recovery journals plus managed files
into staging, validates and consolidates the copy, and publishes a directory
snapshot with a SHA-256 manifest. Restore requires a matching schema, verifies
all files, and publishes to an absent destination using a no-replace rename.
Secrets and deployment settings are preserved separately. See
[BACKUP.md](BACKUP.md) for limits and recovery procedures.

Future multi-replica support requires an external metadata database and shared
object storage; it is not achieved by mounting the SQLite file over NFS.
