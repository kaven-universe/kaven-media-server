# Legacy compatibility

This document is the working contract between the legacy Express backend, the
existing Vue frontend, and Kaven Media Server. Confirm details with automated
fixtures before marking an endpoint complete.

## Reference sources

- Backend routes: `../kaven-image-server/src/app.ts`
- Image behavior: `../kaven-image-server/src/controllers/image.ts`
- Upload behavior: `../kaven-image-server/src/controllers/upload.ts`
- HFS behavior: `../kaven-image-server/src/controllers/hfs/HfsRouter.ts`
- Shared response types: `../kaven-image-server/src/share`
- Frontend consumers: `../kaven-image/src`

## Fixture corpus

Representative legacy requests, responses, image bytes, and filesystem content
are stored under `internal/httpapi/testdata/legacy`. The corpus records its
legacy backend and frontend revisions and distinguishes source-derived values
from installation-specific values that require replay against a configured
legacy deployment.

The development-mode run of the unchanged legacy frontend against these routes
is recorded in [FRONTEND.md](FRONTEND.md). Production asset integration remains
separate from API compatibility validation.

## Route inventory

| Method | Path | Authentication | Legacy behavior | Status |
| --- | --- | --- | --- | --- |
| GET | `/` | none | Text response or embedded UI | Compiled UI in Docker; development placeholder without `webui` tag |
| GET | `/healthz` | none | New diagnostic endpoint | Implemented |
| GET | `/server/info` | none | Upload limits as JSON | Initial implementation |
| GET | `/image/{id}` | optional referer policy | Image bytes or metadata | Implemented |
| GET | `/image/bing/random` | optional referer policy | Random archived Bing image | Implemented |
| GET | `/images` | digest auth | Up to 100 image documents | Implemented when administration is enabled |
| POST | `/images/upload` | legacy allows unauthenticated upload | Multipart image upload | Implemented; admin Digest by default, public opt-in |
| GET | `/hfs/` | digest auth | List configured HFS roots | Implemented when administration is enabled |
| GET | `/hfs/{path...}` | per-root | List directory or serve file | Implemented |
| POST | `/hfs/{path...}?mkdir` | digest auth | Create directory | Implemented |
| POST | `/hfs/{path...}` | digest auth | Upload files | Implemented |
| GET | `/api/sync-bing-images-from-db` | digest auth | Return 202, sync asynchronously | Implemented as canonical archive reconciliation |
| GET | `/api/sync-bing-images-from-dir` | digest auth | Return 202, sync asynchronously | Implemented as canonical archive reconciliation |

The unauthenticated legacy image-upload route needs an explicit security and
compatibility decision before release. Do not expose unrestricted uploads by
accident.

Kaven Media Server keeps this route disabled unless `KAVEN_PUBLIC_UPLOADS=true`
or `--public-uploads` is explicitly set. A disabled route returns HTTP 403 with
error code 1. When enabled, the multipart field remains `images`; single-file
requests return one result object and batches return an array.

## JSON contracts

### Server information

```json
{
  "upload": {
    "maxFileCount": 100,
    "maxImageFileSize": 104857600,
    "maxHfsFileSize": 1048576000
  }
}
```

The existing frontend temporarily accepts both lower-camel-case and
upper-camel-case variants. The canonical new response is lower camel case.

### Upload result

One uploaded file returns one object. Multiple image uploads return an array.

```json
{
  "errorCode": 0,
  "image": {
    "id": "legacy-or-new-id",
    "uuid": "32-character-uuid",
    "name": "original-name.jpg",
    "sha1": "40-character-sha1"
  }
}
```

Stable numeric error codes:

| Code | Meaning |
| ---: | --- |
| 0 | None |
| 1 | Unexpected error |
| 10 | Invalid file type |
| 11 | File already exists |
| 12 | File too large |
| 13 | Folder not found |

### HFS entry

```json
{
  "name": "example.jpg",
  "link": "/hfs/uploaded/example.jpg",
  "isDirectory": false,
  "size": 12345,
  "lastModified": "2026-09-04T00:00:00.000Z"
}
```

`isDirectory`, `size`, and `lastModified` are optional in the TypeScript
contract. Preserve valid ISO timestamp parsing in the frontend.

Compatibility timestamps use the JavaScript `Date.toISOString()` representation:
UTC with exactly three fractional-second digits, for example
`2026-09-04T00:00:00.000Z`.

## Image lookup and transformation

`GET /images` returns an array of at most 100 image metadata documents, or `[]`
when empty. It uses the same field casing, UTC timestamps, and path mapping as
`GET /image/{id}?json`. Query parameters are ignored as in the legacy handler.
Results are ordered by creation time descending, then ID descending. The route
requires admin Digest authentication even when public uploads are enabled and
is absent when administration is disabled. Responses use `Cache-Control:
no-store`; database failures return an empty HTTP 500 response.

Legacy `/image/{id}` dispatches by identifier length:

- 24 characters: MongoDB ObjectId;
- 32 characters: UUID;
- 36 characters: hyphenated UUID accepted by legacy records and migration;
- 40 characters: SHA-1.

With `?json` present, it returns metadata instead of image bytes. Otherwise it
records access and serves the original unless transformation query parameters
are present.

The new lookup parser additionally requires hexadecimal identifiers. It first
queries the exact supplied casing so preserved mixed-case legacy identifiers
remain routable, then retries the normalized lowercase form when needed.
Metadata for newly stored images
reports `path` relative to the data directory rather than exposing an absolute
host filesystem path. The deliberate exception is explicit migration
`reference` mode, whose metadata retains the validated legacy absolute path and
is therefore non-portable.

Original delivery supports HEAD, byte ranges, conditional requests using the
stored SHA-1 as a strong ETag, and filesystem modification dates. Content type
is detected from the opened bytes rather than trusted from the database or
filename. Responses include `nosniff` and a restrictive sandboxed content
security policy, which is especially important for SVG content.

The legacy code recognizes `width`, `height`, and `quality`, but its initial
transformation-presence check mistakenly tests `quantity` instead of `quality`.
The new server supports quality-only requests. Each parameter must occur once
and be a positive decimal integer. Quality is limited to 1-100; requested or
derived dimensions are limited to 10,000 pixels and 100 megapixels. Invalid
parameters return HTTP 400.

The production container uses libvips for metadata, aspect-preserving resize,
same-format encoding, and format-specific quality. JPEG, PNG, WebP, GIF, TIFF,
AVIF, HEIF, and SVG are exercised by a production-build codec test, including
decoding transformed bytes with the final runtime libraries. Native AMD64 and
ARM64 CI builds gate container creation on this matrix; successful execution
on both architectures remains a release acceptance requirement. SVG
transformations are rasterized to PNG and labeled `image/png`. The legacy Sharp
controller does not enable animated input, so transforming an animated GIF
returns its first frame as a one-frame GIF. A deterministic two-frame fixture
captured with the installed legacy Sharp 0.35.4 records this behavior, and the
production codec matrix enforces it. At most four transformations run
concurrently in one process.

Derivative cache keys use the stored image ID and normalized nonzero
`width`, `height`, and `quality` values in sorted order. Identifier aliases,
query ordering, and unrelated query parameters therefore do not create
duplicate derivatives. A cache miss is transformed once per process even when
concurrent requests arrive, then published atomically under `cache/` before its
database row is created. Missing or invalid managed entries are discarded and
regenerated. Cache files are limited to the configured maximum image size.

Every non-metadata request for a recognized image queues an access record with
the stored image ID, original request target, direct peer IP address, and UTC
timestamp. Metadata requests and failed lookups are not recorded. Recording is
deliberately asynchronous: the bounded queue never delays or changes the image
response, each SQLite write has a two-second deadline, and accepted records are
drained for up to five seconds during graceful shutdown. Request targets over
8 KiB and IP strings over 128 bytes are discarded as untrusted audit input.

## Bing metadata

The legacy archive job requests
`/HPImageArchive.aspx?format=js&idx=0&n=100&uhd=1` from Bing and consumes the
lowercase fields `startdate`, `fullstartdate`, `enddate`, `url`, `urlbase`,
`copyright`, `copyrightlink`, `quiz`, `wp`, `hsh`, `drk`, `top`, `bot`, and
`hs`. The new metadata client preserves those fields and accepts opaque JSON
hotspot entries. Fetch windows are limited to 100 images and successful bodies
to 2 MiB.

Archive synchronization fetches the legacy 100-image window and upserts every
unique source URL. Existing valid database files and deterministic orphan files
are reused, making sequential and concurrent reruns idempotent. New responses
are limited to 50 MiB, must be recognized as a supported image from their
bytes, and are atomically published before the database write. A database
failure removes the file created by that attempt. Width and height query
parameters are removed from the download URL as in the legacy service.

`GET /image/bing/random` retains the legacy limit of 20 random selections when
database rows reference missing files. It returns the first available archived
image, supports `HEAD`, byte ranges, and `If-Modified-Since`, and returns 404
when no downloaded image can be opened within that limit. The response MIME
type comes from the file signature rather than its name or stored metadata.

The Bing archive synchronization job runs every 24 hours, matching the legacy
production interval without performing a startup download. Each scheduled job
has a serial worker, so a slow invocation cannot overlap the next one. Process
shutdown cancels active metadata requests, retry waits, and downloads and waits
for the worker before closing SQLite.

The protected legacy maintenance aliases
`GET /api/sync-bing-images-from-db` and
`GET /api/sync-bing-images-from-dir` both enqueue that canonical archive job
and return an empty 202 response immediately. One request can remain pending
while a run is active; further requests are coalesced. The routes are absent
until administrative credentials are configured and responses are marked
`Cache-Control: no-store`.

## HFS behavior and safety

The frontend relies on same-origin requests. It lists `/hfs`, navigates using
the returned `link`, creates folders by posting with `?mkdir`, and uploads each
selected file as multipart field `file`.

The default virtual root is the private name `uploaded`, mapped to
`hfs/uploaded` below the data directory. `KAVEN_HFS_ROOTS` can replace that
default with an ordered JSON array of root objects containing `name`, `path`,
and optional `public`. The root list itself remains administrative. A public
root permits unauthenticated reads through its named URL; it never permits
anonymous directory creation or upload.

Compatibility never overrides containment safety:

- decode each URL segment exactly once;
- reject traversal and absolute-path escape attempts;
- use the basename of uploaded filenames;
- reject existing destination files unless an explicit future overwrite mode
  is designed;
- apply authorization before revealing private directory contents or metadata.

Directory listings preserve the legacy `HFSEntry` shape and percent-encode
each link segment. They expose only regular files and directories and are
bounded at 10,000 entries. Unsafe names, symlinks, and special filesystem
entries are omitted. A requested missing entry returns HTTP 404; malformed,
encoded-separator, traversal, and symlink paths return HTTP 400.

JPEG, PNG, GIF, WebP, TIFF, AVIF, HEIF, and SVG files recognized from their
contents render inline. Recognized audio, video, and PDF content also renders
inline. `.txt`, `.json`, `.log`, `.err`, `.error`, and `.ini` use the legacy
text behavior. Other files, or any file requested with `?download`, use a safe
attachment disposition and `application/octet-stream`. File responses support
HEAD, byte ranges, and modification-time validators and add `nosniff` plus a
sandboxed content security policy.

A successful full or ranged attachment GET queues one download record after
the expected response bytes have been written. The record contains the
configured relative file path, exact request target, direct peer IP address,
optional user agent, and UTC completion timestamp. Inline rendering, HEAD,
304, error, and incomplete responses are not recorded. Recording uses a
bounded asynchronous queue: SQLite latency and insert failures never alter the
file response, and accepted events drain for up to five seconds on graceful
shutdown.

All HFS mutations require admin Digest authentication, even for public roots.
`?mkdir` creates one directory below an existing parent. Multipart uploads use
field `file`, accept at most 100 files, limit each file to the advertised
`maxHfsFileSize`, strip directory components from filenames, and never replace
an existing entry. Files are staged before publication; if any file in a batch
conflicts or publication otherwise fails, files already published by that batch
are removed and pre-existing content remains unchanged. One successful file
returns one `{ "errorCode": 0 }` object; multiple files return an array.

## Authentication and referer policy

The old backend uses digest authentication for protected routes and an allowed
domain/referer middleware for image routes. The new Digest middleware and
protected upload integration and optional image referer filtering are implemented.
`KAVEN_ALLOWED_DOMAIN_NAMES` enables the filter; unset or `[]` leaves image
routes unrestricted. When enabled, empty referers, configured DNS names and
subdomains, localhost, private addresses, and loopback addresses are allowed.
IP allowlist entries match exactly. Rejected requests return HTTP 403 with
`Forbidden` (no body for HEAD). The filter runs before lookup or transformation.

`internal/referer/testdata/legacy.json` records decisions obtained by invoking
the installed legacy middleware with its actual allow-empty/private-IP options.
The contract test covers those decisions; route tests cover GET/HEAD, metadata,
and Bing routing. No live legacy deployment was used for these fixtures.

The legacy sample configuration includes a hard-coded username and password.
Those values are not safe defaults and must not be carried into a fresh
installation. The replacement first-run, Digest SHA-256, secret injection,
authorization, and rotation decisions are specified in
[AUTHENTICATION.md](AUTHENTICATION.md). Administrative routes remain
unmounted until credentials are configured; configured credentials protect
image upload unless public uploads are explicitly enabled.

## Intentional differences log

- Image referer filtering defaults to disabled instead of inheriting the legacy
  sample's author-specific domains. Configuration normalizes ASCII case and
  rejects schemes, ports, wildcards, and invalid names. With filtering enabled,
  malformed or duplicate referers fail with 403 instead of a parsing exception;
  only HTTP(S) URLs without credentials/fragments are accepted. IP literals
  cannot authorize DNS suffixes; private/loopback IPv6 is handled explicitly.
  Responses include `Vary: Referer`, and denials are not cacheable. Covered by
  `TestLegacyContract`, `TestRejectMalformedReferers`,
  `TestPolicyDefaultsAndIPMatching`, and `TestParseConfiguration` in `internal/referer`.

- 2026-09-07, `GET /images`: the legacy MongoDB query specifies no ordering.
  The new endpoint returns the newest creation timestamps first, with descending
  ID as a stable tie-breaker, and disables response caching for administrative
  metadata. Managed paths retain the relative-path policy used by individual
  image metadata. Covered by `TestImagesHandlerLegacyContract` and the image
  list authorization subtest in `internal/app`.

- 2026-09-07, `GET /image/{id}` UUID lookup: imported UUIDs in the standard
  36-character hyphenated form are routable in addition to the legacy handler's
  32-character form. Lookup preserves exact casing first and falls back to
  lowercase, keeping mixed-case imported URLs usable while retaining uppercase
  aliases for lowercase records. Covered by
  `TestLookupPreservesExactCaseAndFallsBackToLowercase` and
  `TestImportRecordsInsertsThenVerifiesEveryRecordType`.

Add dated entries here whenever new behavior intentionally differs from the
legacy service. Each entry should identify the route, old behavior, new
behavior, rationale, and corresponding test.

- 2026-09-04, `GET /image/{id}?quality=N`: quality-only transformation is
  supported; the legacy `quantity`/`quality` presence check is considered a
  bug. Covered by `TestImageHandlerServesTransformation`.
- 2026-09-04, `GET /image/{id}` transformations: malformed, repeated, or
  resource-exceeding parameters return HTTP 400, and transformed SVG is served
  as PNG rather than mislabeled SVG. These safety and content-type corrections
  are covered by `TestImageHandlerRejectsInvalidTransformationParameters`,
  `TestProcessorValidatesTransformLimits`, and `TestLibvipsCodecMatrix`.
- 2026-09-04, transformed-image caching: the legacy server keys its cache by
  the literal request URL. The new server canonicalizes identifier aliases,
  parameter order, and unrelated parameters to prevent duplicate files and
  redundant work. Covered by `TestServiceCreatesAndReusesCanonicalCache` and
  `TestServiceCoalescesConcurrentMisses`.
- 2026-09-04, `GET /image/{id}` access records: the legacy handler awaits the
  database insert before starting delivery, so an audit failure can fail or
  delay an otherwise valid request. The new server uses a bounded asynchronous
  recorder and permits audit loss under saturation to keep delivery isolated.
  Covered by `TestImageHandlerRecordsByteRequestsWithoutChangingResponse`,
  `TestRecorderNeverBlocksWhenQueueIsFull`, and `TestRoutes/image_access_is_persisted`.
- 2026-09-04, `POST /images/upload`: legacy installations accept anonymous
  uploads by default; the new server requires an explicit public-upload opt-in
  to prevent a fresh internet-facing instance from becoming an unrestricted
  file-ingestion endpoint. Covered by `TestUploadHandlerDisabledByDefault`.
- 2026-09-04, `GET /image/{id}?json`: malformed identifiers with a recognized
  length are rejected with HTTP 400 rather than reaching a database cast error,
  and the `path` field is data-directory-relative to avoid leaking host paths.
  Covered by `TestParseIdentifierRejectsMalformedValues` and
  `TestImageHandlerMetadataByEveryIdentifier`.
- 2026-09-04, `GET /image/{id}`: a database row whose file is not a recognized
  image returns HTTP 415 instead of serving attacker-controlled bytes using a
  stored MIME type. Original responses add `nosniff` and a sandboxed content
  security policy. Covered by
  `TestImageHandlerRejectsMissingUnsafeAndInvalidStoredFiles`.
- 2026-09-04, protected routes: the legacy example ships reusable credentials
  and legacy Digest behavior. A fresh new instance mounts no protected routes
  until an explicit secret is configured, and challenges accept SHA-256 only.
  Digest validation, nonce expiry, replay rejection, same-origin authorization,
  and upload route policy are covered by `internal/auth` tests and
  `TestUploadAuthorizationPolicy`. See [AUTHENTICATION.md](AUTHENTICATION.md).
- 2026-09-04, `GET /hfs`: the legacy configuration can map virtual roots to
  arbitrary absolute host paths. New roots must be non-overlapping relative
  paths below the persistent `hfs/` directory, preserving the single-volume
  deployment and preventing configuration from exposing the host filesystem.
  The deliberate exception is an explicit migration reference configured with
  `readOnly: true`; every requested component is checked for symlinks and no
  mutation route is registered. Public roots grant reads only. Covered by `internal/hfs` tests and
  `TestHFSRootListingRequiresConfiguredAdministrator`.
- 2026-09-04, `GET /hfs/{path...}`: the new server rejects unsafe URL segments
  before router normalization, never follows configured-tree symlinks, omits
  special entries, bounds directory listings, and downloads unrecognized or
  active content instead of relying on filenames or browser sniffing. Covered
  by `TestHFSPathHandlerRejectsUnsafeAndMissingPaths`,
  `TestRegistryRejectsUnsafeSegmentsAndRequestedSymlinks`, and
  `TestHFSPathRoutesApplyPerRootReadPolicy`.
- 2026-09-04, HFS mutation: the legacy multer flow accepts arbitrary file-field
  names and can leave earlier files behind when a later file conflicts. The new
  server requires field `file`, rejects empty uploads, stages the complete
  batch, publishes without overwrite, and rolls back request-owned files after
  any failure. Directory creation requires an existing parent instead of
  recursively creating a client-supplied chain. Covered by
  `TestHFSWriteHandlerRejectsInvalidAndOversizedUploads`,
  `TestHFSWriteHandlerPreservesExistingFileAndRollsBackBatch`, and
  `TestRegistryCreatesOneDirectoryWithoutOverwriting`. Encoded traversal,
  symlinked mutation targets, concurrent same-name publication, overwrite, and
  interrupted multipart cleanup have dedicated adversarial coverage in the
  `internal/httpapi`, `internal/hfs`, and `internal/storage` test suites.
- 2026-09-04, HFS download records: the legacy callback starts its database
  write after a successful attachment send. The new server additionally
  verifies that the complete advertised response body was written, records
  successful range transfers, and uses a bounded asynchronous queue that may
  drop audit events under saturation. Covered by
  `TestHFSPathHandlerRecordsOnlyCompletedDownloads`,
  `TestHFSPathHandlerDoesNotRecordFailedResponse`, and
  `TestHFSDownloadIsPersistedAfterCompletedTransfer`.
- 2026-09-04, Bing metadata retrieval: the legacy job uses plain HTTP and the
  helper's implicit transport behavior. The new client defaults to HTTPS,
  applies a ten-second deadline per attempt, and makes at most three attempts
  for transport failures, 408, 425, 429, and 5xx responses. Exponential delays
  and `Retry-After` are capped at five seconds; permanent HTTP and payload
  errors are not retried. Covered by `internal/bing` client tests.
- 2026-09-04, Bing archive synchronization: the legacy filename contains
  copyright text and falls back to the current year when dates are malformed.
  New files use a SHA-256 of the source URL plus the detected extension and use
  `bing/unknown` for malformed dates, producing stable safe paths across
  metadata and calendar changes. Downloads and redirects are restricted to the
  configured Bing origin, capped at 50 MiB, signature-validated, and published
  before a short database upsert with owned-file rollback. Covered by
  `TestServiceDownloadsPersistsAndReusesBingImage`,
  `TestServiceAdoptsValidOrphanWithoutDownloading`, and the remaining
  `internal/bing` service tests.
- 2026-09-04, `GET /image/bing/random`: the legacy handler passes the database
  path directly to `sendFile`. The new handler serves only regular,
  signature-recognized files below the managed `bing/` directory or validated
  absolute paths created by explicit migration reference mode; it rejects
  traversal and symlinks. Covered by
  `TestBingImageHandlerRejectsUnsafeAndInvalidStoredFiles`.
- 2026-09-04, scheduled Bing synchronization: the legacy development mode
  performs an extra synchronization at startup. The new server consistently
  waits for the first daily interval, preventing startup network activity, and
  cancels and joins active work during shutdown. Covered by `internal/scheduler`
  tests and `TestRunCreatesLayoutAndStopsOnCancellation`.
- 2026-09-04, manual Bing synchronization: the legacy aliases scan database
  rows or filenames and rename legacy files, including deleting duplicate
  MongoDB documents. SQLite enforces unique URLs and hashes, while new and
  imported file layout belongs to archive synchronization and migration. Both
  aliases therefore enqueue the bounded canonical archive job without deleting
  records or parsing filenames. Covered by
  `TestBingSyncHandlerReturnsAcceptedForQueuedAndCoalescedRequests`,
  `TestSchedulerTriggerRunsImmediatelyAndCoalescesPendingRequests`, and
  `TestBingSyncRoutesRequireAdministrator`.
