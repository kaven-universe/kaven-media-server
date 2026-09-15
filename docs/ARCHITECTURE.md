# Architecture

## Goals

Kaven Media Server is a single-process Go service for image hosting, image
transformation, Bing wallpaper archiving, and lightweight file hosting. It is
designed to run from one Docker image with one persistent host directory
bind-mounted at `/data` and no external database.

Primary goals:

- zero-config first start;
- stable application-owned APIs and frontend behavior;
- durable, recoverable local storage;
- bounded resource use for untrusted uploads and image transformations;
- a structure that remains understandable without framework-specific magic.

Non-goals for the first stable release:

- active/active replicas sharing one SQLite file;
- storing image binaries inside SQLite;
- importing foreign database schemas at runtime;
- discovering or rewriting filesystem layouts automatically.

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
paths relative to the data directory wherever possible so the host directory
can be moved or restored at a different path.

Application settings are stored in SQLite. They include the relative upload and
download directories, anonymous upload access, upload limits, remembered-login
duration, the image referer allowlist, ordered HFS roots, and the Bing automatic synchronization policy. Setup writes
credentials and initial settings atomically;
later changes are validated as one replacement and loaded by a brief internal
restart. Listen address, data directory, proxy cookie transport, and emergency
administrator credentials remain process-start deployment settings.

## Persistent layout

`upload` and `download` are configurable relative directory names; these
are their defaults. Original-image and Bing database references are relative to
their configured media roots, so the absolute data path is never persisted.

```text
/data/
├── kaven-media.db
├── upload/
├── cache/
├── download/
│   └── bing/
├── tmp/
└── <operator directories>/
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

A fresh instance exposes a one-time setup page that atomically stores the first
administrator's password verifier in SQLite. Environment-managed credentials
override that verifier. The setup, password login, ephemeral and optional
SQLite-backed remembered sessions, TLS, rotation, and route-authorization contract is defined in
[AUTHENTICATION.md](AUTHENTICATION.md).

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

Image IDs are stored as `TEXT`, which allows imported identifiers and stable
`/image/:id` URLs without an ID translation table. New uploads use UUID-based
text IDs. Relative HFS roots resolve below the data directory, while absolute
roots can address external mounts. Every path component is checked for symlinks,
and each root independently enables or disables mutation routes.

## Files and consistency

For new uploads:

1. Stream into `/data/tmp` with a hard byte limit.
2. Inspect magic bytes; do not trust the supplied content type or extension.
3. Decode metadata with dimension and pixel-count limits.
4. Calculate the content SHA-1 used by image lookup and integrity checks.
5. Move the file atomically into its final location.
6. Insert the database row in a short transaction.

If the database insert fails after the rename, remove only the newly created
file whose ownership is known. The `check` command must detect orphaned files,
missing files, invalid cache entries, and broken foreign-key relationships.

## Image processing

The image processor uses libvips for broad codec support and predictable
performance. The Docker build must explicitly
package the required runtime codecs and test JPEG, PNG, WebP, GIF, TIFF, AVIF,
HEIF, and SVG behavior rather than assuming the library build supports them.

Libvips has process-wide lifetime because govips cannot initialize it again
after shutdown. Application-level restarts may replace the processor and HTTP
stack, but only final `serve` command shutdown releases the libvips runtime.

Cache keys must be canonical. Parse and validate width, height, quality, and
output format before constructing a cache key. Deduplicate concurrent requests
for the same missing derivative so only one transformation is performed.

Image access audit writes run through a bounded single-worker queue. Delivery
must not wait for SQLite or fail because an audit insert failed. Graceful
shutdown drains accepted events within a fixed deadline; saturation may drop
audit events rather than applying unbounded memory growth or request backpressure.

## Bing archive

The Bing metadata boundary is a small standard-library HTTP client. It calls
the homepage archive resource over HTTPS, applies a deadline to every
attempt, limits response bytes and decoded collection sizes, and retains the
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
from their signature, and atomically published under `download/bing/<year>/`. The SQLite
upsert happens only after publication; a failed upsert removes only a file
created by that attempt. Per-image failures are collected while independent
images continue.

Random delivery uses a 20-selection bound for stale database references. Each
candidate path must remain below `download/bing/`. Symlinks
and non-regular files are rejected, and the opened handle is rechecked against
the validated path before signature detection. Successful responses use the
detected MIME type and standard `ServeContent` range and modification-time
semantics.

The process scheduler owns one serial worker loop per named job. Enabled jobs
wait for their first interval rather than delaying startup, and a job cannot overlap its
previous invocation. Shutdown cancellation is passed into active work, all job
workers return before repositories and SQLite are closed, and failures are
logged without stopping future intervals. Bing archive synchronization defaults
to enabled once every 24 hours. Administrators can disable automatic runs or
select a 1-8,760 hour interval; saving the setting restarts the internal runtime
so the new schedule takes effect.

The authenticated versioned manual route feeds the named scheduler job through
a capacity-one trigger. A request starts the job promptly when it
is idle, keeps at most one run pending while it is active, and coalesces excess
requests. Manual runs remain available when automatic scheduling is disabled.
This bounds memory and goroutine use while retaining an asynchronous 202 response.

## HFS roots

HFS is a virtual URL namespace, not a physical storage directory. Virtual HFS
roots map URL-safe names to non-overlapping server directories. Relative paths
resolve below the data directory, so the default private `uploaded` mapping of
`upload` resolves to `<data-dir>/upload`; no physical `hfs` directory is implied.
Absolute paths can address separate Docker mounts such as `/hfs/pub`.
`readOnly` has identical meaning for every path: it
disables folder creation and upload, while a writable root exposes those
operations only to the administrator. Private `backup`, temporary, and
internal operation directories are rejected.

Every configured directory must exist when settings are saved. If any
configured mount is missing during a later process start, the runtime logs and
omits that root instead of preventing the server from starting, regardless of
its write policy. Its stored setting remains available for correction. Unsafe
paths remain fatal. A public root permits unauthenticated reads only.

HFS URL segments are decoded exactly once and validated before route
normalization or filesystem access. Opens reject symlinks and verify that the
opened handle still identifies the validated path. Directory listings include
only regular files and directories, omit unsafe names and special entries, and
are capped at 10,000 entries. File responses use signature-based media
detection for supported images and a small text-extension allowlist;
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
events. Records store the resolved absolute file path, original request
target, direct peer IP, optional user agent, and UTC completion time.

## HTTP and UI

An optional bounded domain allowlist filters Referer on image and Bing routes
before lookup or transformation. Empty referers and local/private IP literals
are allowed; malformed headers fail closed when enabled. Filtering
is disabled by default and is not authentication. Enabled responses vary on
Referer to keep shared caches from mixing decisions between referring sites.

Use `net/http` routing and middleware. Administrative and diagnostic APIs are
versioned so their contracts can evolve deliberately.

The compiled Quasar/Vue UI can be embedded with `go:embed`, giving the Docker
image one server and one origin. During development, the UI may run separately
with a proxy to the Go server. The application now owns its frontend source in
`web/`, including the shared API types, so builds need no sibling
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

The administrative Settings page can run the same read-only integrity checker
as the CLI against the active database and data directory. The versioned API
serializes concurrent checks, returns bounded issue details, and reports
missing or unsafe database references, checksum mismatches, invalid database
relationships, missing managed directories, and orphaned managed files. It
does not inspect user-managed HFS content.

Original-image and Bing reads use the configured upload directory and the
`bing/` child of the configured download directory. The defaults are
`upload/` and `download/bing/`. Cache references must be below `cache/`. Every
path remains beneath the configured data root and passes the same containment
and symlink checks. Stored paths are used exactly as recorded; the application
does not guess, rewrite, or search for alternate file locations.

The one-time initialization page appears before the normal UI until an
administrator exists. The administrative backup page creates and restores
snapshots only through the private `<data-dir>/backup/` repository. This
repository is not routed through HFS. Operators may populate it through a
separate secure filesystem transfer. The server reads selected snapshots
directly and invokes the same checksum, producer, schema, and database-integrity
validation as CLI restore. The restore wizard identifies managed directories
that remain in place, reads a bounded symlink-free directory inventory below
`/data`, and requires the administrator to select or review HFS virtual-root
mappings. Any safe directory below `/data` is offered as an absolute path;
external absolute paths remain available for manual entry. The original ordered HFS
roots are read from a validated temporary copy of the selected backup and are
the default, so an unchanged deployment requires no remapping. Before publishing the candidate database, Web restore retains the
active administrator credential and sessions and writes the reviewed HFS roots;
other application settings remain those in the snapshot. It then shuts down its
HTTP and background resources, moves only the
active SQLite files into a private rollback directory, moves the candidate
database into place, and starts again in the
same container. Small durable phase files make each move resumable after an
abrupt process or host restart. The routes are absent until administration is
initialized.

Creating a private snapshot records a bounded, validated request and performs
the existing offline backup operation between application lifecycles, after the
database and background writers have closed. Snapshots are direct children of
`backup/`, may coexist, and contain only a consolidated SQLite database plus its
manifest. Failed creation is recorded for the next authenticated UI session without
preventing the server from returning to service.

## Deployment boundary

Container build targets cover Linux AMD64 and ARM64. The frontend stage uses
the builder architecture; the Go/CGO build and native tests use the target
architecture. Build and runtime stages share an Alpine release and explicitly
include the HEIF loader. Before an image can finish building, its runtime
libraries must pass the codec matrix under the runtime user. Native CI runners
also check the built architecture and exercise the running HTTP server.

The supported default is one container and one local bind-mounted directory.
Offline database backup holds the same OS-level data-directory lock as the
server, importer, and checker. It copies SQLite and recovery journals into
staging, validates and consolidates the copy, and publishes a database snapshot
with a SHA-256 manifest. Restore requires a matching schema, verifies the
database, and publishes to an absent destination using a no-replace rename.
Managed file trees are retained in place for Web restore and must be copied or
mounted separately for CLI restore. External secrets and container or host
deployment settings are preserved separately. See
[BACKUP.md](BACKUP.md) for limits and recovery procedures.

Future multi-replica support requires an external metadata database and shared
object storage; it is not achieved by mounting the SQLite file over NFS.
