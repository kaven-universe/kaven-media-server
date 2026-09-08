# Legacy frontend integration

## Compatibility run

The existing Quasar/Vue frontend was run without application-source changes
against the Go compatibility API on 2026-09-04. The revisions under test were:

- frontend `74fe40c6ae9daaea996d5f45ed524b7f31dadc91`;
- legacy shared contracts `81de1bf92f9e76079203bd0a4e0175f404a4f9c4`;
- Kaven Media Server `4c23539427b58865f6092b188d8030a301c46c9d`.

The frontend checkout intentionally ignores `src/share`. As in its legacy
development workflow, that generated directory was populated from
`../kaven-image-server/src/share` in a disposable copy before Quasar started.
No tracked frontend source was edited.

The development server loaded the SPA and issued its startup
`GET /server/info` through the configured proxy. The response used the expected
lower-camel-case upload limits. The following frontend request contracts were
also exercised through that same proxy:

| UI operation | Request | Result |
| --- | --- | --- |
| Load limits | `GET /server/info` | `200`, limits parsed by the current store |
| Upload image | `POST /images/upload` with field `images` and UUID form data | `200`, `errorCode: 0` and image identity |
| Open uploaded image | `GET /image/{sha1}` | `200`, detected `image/svg+xml` bytes |
| Open public HFS root | `GET /hfs/uploaded` | `200`, compatible entry array |

Protected HFS root discovery and mutation still use the browser's Digest
authentication challenge. Their handler-level challenge and authorization
contracts are covered by the Go test suite; they were not treated as
unauthenticated UI operations during this run.

Quasar reported its existing Options API mixin warning, but no application
compile or runtime error remained after the normal shared-contract generation
step. This run validates development-mode compatibility only. The production
bundle and embedded-asset workflow are tracked separately.

## Request origin

The active frontend components use relative URLs for `/server/info`,
`/images/upload`, `/image`, and `/hfs`, so development uses the Quasar proxy and
production can use the server's origin.

## Owned frontend source

On 2026-09-07, the tested frontend revision and the legacy shared-contract files
were imported into `web/`. This is now the source location for Kaven Media
Server UI changes; neither sibling repository is needed to build it. The
dependency lockfile and original source notices are retained.

The toolbar, document title, description, package metadata, and active favicon
now identify Kaven Media Server. `web/src/boot/axios.ts` configures `$api` with
`baseURL: "/"`, removing the unused external template API origin. Existing
relative component requests are unchanged. The development proxy also handles
`/api` maintenance routes and no longer automatically opens a browser.

The production SPA builds to `web/dist/spa`. TypeScript checks, lint, and build
commands are documented in [`web/README.md`](../web/README.md). The Dockerfile
now builds and embeds this bundle; Go builds without `webui` continue to serve
the development placeholder.

Verification on 2026-09-07 used Node.js 24.18.1 and pnpm 11.8.0. Frozen-lockfile
installation reused the local package cache and left the imported lockfile
byte-identical. Production build, standalone TypeScript checks, and ESLint
passed. An HTTP smoke check started the Go server with disposable data and
verified the branded development page, same-origin Axios module, proxied server
information, public HFS listing, disabled administrative API response, image
upload, and exact downloaded SVG bytes. The Go tests and vet also passed.
No browser was connected for a rendered-page check; browser runtime and visual
verification of this branded source remain unverified.

## Production embedding

The Docker frontend stage uses Node.js 24 and pnpm 11.8.0 with the frozen
lockfile, then runs the Quasar build including its type and lint checks. Its
output is copied directly into the Go build stage before tests and compilation
with `libvips,webui`. Generated files are excluded from both Git and the host
Docker context, preventing stale local output from replacing the container's
bundle. The fallback page lives separately in `internal/webui/fallback`.

The tagged Go tests require a compiled index, verify its asset references,
compare the generated tree with the embedded tree, and check HTTP byte delivery
for every embedded file. Missing files, directory listings, and unknown API
paths return 404. CI builds and starts the final image and requests its health
endpoint, index, JavaScript entry point, and missing/private routes.

Docker is unavailable on the local Windows host, so the full container build
and startup checks must run in CI. This is separate from the locally verified
frontend build and tagged Go tests.

Local verification passed `go test ./...`, `go test -tags=webui ./...`, and vet
with and without `webui`. A standalone tagged executable, started from a
disposable working directory with fresh data, served the
compiled index, entry-point JavaScript, favicon, and server-info API. The smoke
check also verified cache headers and 404 responses for asset directories,
missing assets, and unconfigured administrative routes.
