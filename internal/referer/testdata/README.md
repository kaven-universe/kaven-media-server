# Legacy referer decisions

`legacy.json` captures decisions from the installed legacy
`kaven-utils/src/KavenUtility.Server.js` export `CreateExpressCheckReferer`,
invoked through Node with `["example.com"]`, `allowEmpty: true`, and
`allowPrivateIP: true`. These options match the legacy server's
`src/middleware/index.ts`. The source repositories were read-only.

The harness supplies `req.get("Referer")`, records `res.sendStatus`, and records
200 when `next()` runs. These are middleware decisions, not captures of a live
MongoDB-backed server. Express's denial is `403 Forbidden`. The Go contract
test also checks the new cache headers and denial body.

Malformed and duplicate headers, IPv6 handling, disabled filtering, normalized
configuration, and IP suffix rejection are covered separately as explicit
validation behavior. See `docs/COMPATIBILITY.md` for intentional differences.
