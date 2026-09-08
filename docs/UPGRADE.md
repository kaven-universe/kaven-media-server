# Upgrade and rollback

Use one server process and a local data volume. Upgrades require downtime;
rolling upgrades against one SQLite directory are unsupported. This project
has not declared v1: review the [release acceptance criteria](RELEASE.md) before
deploying a candidate.

## Prepare

Run `kaven-media version` and record its JSON output along with the exact image
ID, deployment
configuration, HFS mappings, secret locations, and actual volume name. Compose
normally prefixes named volumes with its project name; do not assume the YAML
volume key is the Docker volume name. Keep the old executable/image available.

Build the candidate separately using [the production build](DEVELOPMENT.md#container-architectures),
including the UI and libvips. Use distinct version tags and record image IDs;
do not rebuild over the only copy of the old image. No published registry or
release tag is assumed by this guide. Review changes to configuration, routes,
schema migrations, native libraries, and deliberate compatibility differences.

Reserve space for the backup, a restored rehearsal copy, and subsequent
uploads/cache growth. Capacity is not checked automatically before upgrades.
Keep backup storage outside the application data directory.

## Back up and rehearse

1. Stop the old service and all writers, including external programs accessing
   its files. Leave public traffic in maintenance mode until acceptance passes.
2. Run `check` and `backup` with the **old matching version** and the server's
   HFS configuration. Require successful exit statuses. Investigate integrity
   errors before continuing; these commands do not repair damaged data.
3. Restore with that same version into a new, absent directory. Use the
   [backup guide](BACKUP.md) for ownership, limits, and fresh-volume Docker
   commands. Format 2 snapshots reject a known producer version or revision
   mismatch before copying. Keep the original volume and backup untouched.
4. Run the candidate's `check` against the restored copy. It opens SQLite and
   applies pending embedded migrations before checking integrity. `serve` also
   applies migrations; neither command is a read-only schema probe.
   Use `--json`, retain the report, and verify its producer revision and ordered
   `schemaVersions`. Compare its `tableCounts` with the pre-upgrade integrity
   evidence and account for any rows intentionally added by migrations.
5. Start the candidate on the restored copy with the intended configuration,
   initially on an isolated port. Verify the UI, representative image IDs and
   transformations, authentication, and public/private HFS behavior. Stop it
   and run its `check` again after exercising writes.

Example for native executables, from an installation directory with `data/`:

```sh
mkdir -p backups
./kaven-media-old check --data-dir ./data
./kaven-media-old backup --data-dir ./data --output ./backups/pre-upgrade
./kaven-media-old restore --input ./backups/pre-upgrade --data-dir ./candidate-data
./kaven-media-new check --data-dir ./candidate-data --json
./kaven-media-new serve --data-dir ./candidate-data --listen 127.0.0.1:5559
```

The executable names are placeholders for the saved old and candidate builds.
Run each command only after the preceding command succeeds. `serve` stays in
the foreground; stop it before the final integrity check. Reapply credentials
and HFS settings before starting either version. Reference-mode legacy data
cannot use the self-contained backup workflow; resolve external storage before
using this procedure.

## Cut over

With both servers stopped, configure the production service to use the
candidate image/executable and the validated restored directory or volume.
For the Docker restore layout, set `KAVEN_DATA_DIR=/data/restored` and mount the
new volume at `/data`. Preserve secret mounts and HFS mappings. Start exactly
one process, verify health and the user flows below, then enable traffic.

Keep the original data offline through acceptance. Any uploads or changes made
after cutover exist only in the new volume. Take a new backup using the new
version after acceptance, and retain the pre-upgrade backup with its old build
and recorded `version` output.

## Roll back

Stop the candidate and disable traffic first. Preserve its volume for diagnosis
and any post-cutover data recovery. Point the deployment back to the saved old
image/executable, original volume, and matching configuration, then restart and
verify it before reopening traffic.

If the original volume is unavailable, use the old matching executable to
restore the pre-upgrade backup into another absent directory or fresh volume.
Check it with the old executable, then configure the old service to use it.

There are no down migrations. Never start an older executable against the
upgraded database. Current builds reject recorded migration versions that are
not an ordered prefix of their embedded migrations before changing journal
mode or applying schema changes. Older builds may lack this guard; it is not a
schema downgrade or a check of every table definition. Do not overwrite the new
volume with the backup.
Rollback restores the pre-upgrade state and does not carry later uploads or
audit records back automatically. Reconcile those separately before deleting
any retained data. Do not use `docker compose down -v` during this procedure.

## Acceptance checks

- `/healthz` returns success and the branded UI loads its JavaScript assets.
- Original URLs resolve by ObjectId, UUID, and SHA-1 with expected bytes;
  representative resize/quality requests return decodable images.
- The configured administrator can list images and use intended HFS operations;
  unauthenticated private access remains denied.
- Public uploads and public HFS reads are exposed only where configured.
- After stopping the service, the candidate's `check` succeeds; restart and
  verify previously written data remains accessible.

A health response alone does not establish codec, schema, or storage parity.
