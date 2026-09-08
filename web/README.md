# Kaven Media Server frontend

This repository owns the Quasar/Vue UI under `web/`. It retains the legacy
image uploader and HFS browser, with Kaven Media Server branding and same-origin
API requests. The shared API types and helpers are checked in under `src/share`;
building no longer needs either sibling repository.

## Development

Use Node.js 24 and pnpm 11.8.0 (pinned in `package.json`). From this directory:

```sh
pnpm install --frozen-lockfile
pnpm dev
```

Run the Go server separately on `127.0.0.1:5558`. Quasar proxies `/image`,
`/images`, `/server`, `/hfs`, and `/api` requests to that server. The dev server
does not open a browser automatically. Uploads and private HFS operations keep
the backend's credential requirements; use the settings documented in
[`docs/AUTHENTICATION.md`](../docs/AUTHENTICATION.md).

Both the `$api` Axios instance and existing relative requests use the page's
origin. Production assets must be served at `/` on the same origin as the Go
API. Hash routing preserves `/hfs` as an API route while the file-browser page
uses `/#/hfs`.

## Verification and build

```sh
pnpm typecheck
pnpm lint
pnpm build
```

`pnpm build` also runs the configured TypeScript and ESLint checks. It produces
`dist/spa`, which is ignored by Git. The root Dockerfile builds this bundle,
copies it to `internal/webui/dist`, runs the embedded-asset checks, and builds
Go with `-tags=libvips,webui`. Node and pnpm are build tools only; the final
container runs the Go executable. Plain Go builds without `webui` retain a
development placeholder. See the [local embedding commands](../docs/DEVELOPMENT.md#ui-workflow).

## Provenance

The initial source import comes from `kaven-image` revision
`74fe40c6ae9daaea996d5f45ed524b7f31dadc91`. The four `src/share` files come from
`kaven-image-server` revision `81de1bf92f9e76079203bd0a4e0175f404a4f9c4`.
Original source notices are preserved. Dependencies and their versions were
retained from the frontend's lockfile. Both legacy repositories remain
read-only behavioral references; future UI work belongs here.
