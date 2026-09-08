# Backup and restore

`backup` and `restore` are offline commands for single-process, local-directory
deployments on Linux and Windows. Restore using the matching server version,
then upgrade separately. Restore requires exactly the executable's known schema
migrations and never applies migrations itself.

Docker operators who cannot run commands can restore from the authenticated web
UI instead. Open **Restore**, select the snapshot directory itself, review its
file count and size, and confirm. The selected directory must directly contain
`manifest.json` and `data/`. The browser uploads the files without modifying the
active data. The server reconstructs and fully validates the snapshot in private
staging, responds only after a valid candidate is durable, briefly restarts its
application lifecycle, applies the candidate, and returns to the upload page.
Keep the browser open until it reconnects.

For a container deployed with a host bind mount, the validated swap happens
inside the directory mounted at `/data`; it does not replace the mount itself.
No Docker command is needed for UI restore. Other programs must stop writing to
that host directory until the server has restarted and the restored data is
verified.

The restore operation at `POST /api/v1/admin/restore` is available only when
administrator credentials are configured. The upload is limited to the same 100,000 snapshot
entries, 1 TiB total file bytes, and 16 MiB manifest as CLI restore. Only one UI
restore may run at a time. Validation failure removes owned staging and leaves
the active database and files unchanged. A power loss after validation resumes
the recorded swap before SQLite is reopened on the next process start.

Format 2 backup manifests embed the producing `kaven-media version` identity,
and successful command output repeats that identity. Keep the output with the
deployment records as an independently retained reference.

## Create a backup

Stop the server and any check command first. Keep all other programs from
writing to the data directory until backup finishes, including older server
versions that predate the data-directory lock. Create the backup parent first:

```sh
kaven-media backup --data-dir ./data --output ./backups/snapshot-2026-09-07
```

The output directory must not exist and must be outside the source data tree.
Success prints JSON with the published path, file count, total file bytes,
manifest version, creation time, and producer identity; failure exits nonzero.
The default aggregate limit is 1,099,511,627,776 bytes
(1 TiB); override it with `--max-bytes N`. Both commands allow at most 100,000
files/directories, 100,000 database file references, and a 16 MiB manifest. File copying and hashing stream through
bounded buffers, directory reads use bounded batches, and cancellation is
checked during scans and copying.

After inventory, backup checks available space on the destination filesystem
before creating its private staging directory. The
required floor is the total regular-file bytes plus 64 MiB for the manifest,
SQLite recovery/consolidation, filesystem metadata, and temporary overhead.

The snapshot is an ordinary directory suitable for copying to separate storage:

```text
snapshot-2026-09-07/
├── manifest.json
└── data/
    ├── kaven-media.db
    ├── images/
    ├── cache/
    ├── bing/
    └── hfs/
```

All four managed trees and their empty directories are included. Temporary
uploads, the process lock, and SQLite shared-memory files are omitted. WAL or
rollback journals are copied when present and recovered/consolidated only in
the staged copy. Source SQLite bytes are unchanged; the snapshot ends with one
standalone database file. Unknown top-level data entries are rejected rather
than silently excluded.

Backups fail on missing references, invalid caches, database or foreign-key
errors, orphaned managed images, symlinks, special files, and unsafe/non-portable
paths. Run `check` while stopped to diagnose issues; backup does not delete or
repair source data. Ordinary file bytes and modification times are preserved.

Image and Bing references must point into managed storage. The CLI also rejects
external HFS roots declared in `KAVEN_HFS_ROOTS`. Run it
with the same HFS configuration as the server; undeclared external mounts cannot
be discovered or included. Copy those files into managed storage first.

## Restore into a new directory

```sh
kaven-media restore --input ./backups/snapshot-2026-09-07 --data-dir ./restored-data
kaven-media check --data-dir ./restored-data
kaven-media serve --data-dir ./restored-data
```

The destination parent must exist and the destination itself must be absent,
even if an existing directory is empty. There is no overwrite option. Restore
validates the manifest, paths, file inventory, sizes, SHA-256 checksums, schema,
producer identity, and database/file integrity before publishing. A known
producer version or revision must exactly match the restoring executable; a
mismatch fails before staging or copying. Restore also requires the snapshot's
total regular-file bytes plus the same 64 MiB reserve to be available before it
creates staging content. A private sibling staging
directory keeps writes on the destination filesystem. A no-replace rename
publishes the complete result. Linux synchronizes directory metadata as well as
file contents; Windows flushes file contents and requests write-through rename.

Files use mode 0600 and directories mode 0700 where supported, with ownership
belonging to the account running restore. Run restore as the server's account.
File modification times are preserved and an empty `tmp` directory is recreated.
Deployment settings and secrets are not included: preserve Compose configuration,
HFS root mappings, secret mounts, and exact server/image version separately and
reapply them before starting the restored service.

Normal failures and cancellation remove owned staging files and leave existing
destinations unchanged. Abrupt termination or power loss may leave a
`.kaven-stage-*` sibling; it is incomplete and may be removed manually after
confirming no operation is using it. Retry with an absent destination. If rename
succeeded but Linux parent-directory synchronization failed, the error explicitly
says the destination was published: inspect it with `check` before further action.

The capacity check is a planning floor rather than a reservation. Free space
can change immediately afterward, and filesystem or SQLite overhead can exceed
the reserve, so operators should retain additional headroom.

Keep the previous installation and backup until the restored service is verified.
Rollback means stopping the restored service and starting the old directory with
its matching server version and configuration.

## Docker workflow

Create a host `backups` directory writable by the image's `kaven` user. Then:

```sh
docker compose stop kaven-media
docker compose run --rm --no-deps -v "$PWD/backups:/backups" kaven-media backup --data-dir /data --output /backups/snapshot-2026-09-07
docker compose start kaven-media
```

Check the backup command's exit status before treating the snapshot as successful.
For command-line restore, use the same explicitly tagged image and a fresh host
parent directory writable by the image's `kaven` user:

```sh
mkdir -p restore-work
docker run --rm \
  --mount type=bind,src="$PWD/restore-work",dst=/restore \
  --mount type=bind,src="$PWD/backups",dst=/backups,readonly \
  kaven-media-server:your-version restore \
  --input /backups/snapshot-2026-09-07 --data-dir /restore/data
docker run --rm \
  --mount type=bind,src="$PWD/restore-work/data",dst=/data \
  kaven-media-server:your-version check --data-dir /data
```

The image tag above is the tag chosen for your build. Restore publishes the new
`restore-work/data` directory atomically. Configure the service to bind that
host directory at `/data`. Only one server may use the restored directory; all
durable state remains under that one host path. Full Docker execution requires
container verification; local tests cover the commands and storage operations.

## Format and locking

Format version 2 adds the producer's version, revision, modified-tree flag, and
Go version to the format 1 JSON structure. Restore continues to accept format 1
snapshots, whose producer cannot be verified, for backward compatibility. Both
formats use UTC creation time and lexically sorted entries. Paths use forward
slashes. Files record size, lowercase SHA-256, and modification
time; directories are explicit. Manifest times use RFC 3339 fractional-second
precision, independently of repository timestamp formatting. Checksums detect
damage; the manifest is neither signed nor encrypted, so producer matching is
an operator-error guard rather than authentication. Protect the whole snapshot
like the original data.

`serve`, `check`, and `backup` share an OS-level exclusive
lock on `.kaven-media.lock`. Contention fails promptly. Process exit releases the
lock automatically: a leftover file is normal and must not be deleted to bypass
an active process. This does not support shared network filesystems or replicas.

SQLite's [backup consistency guidance](https://www.sqlite.org/howtocorrupt.html#backup_or_restore_while_a_transaction_is_active)
explains why copying during a transaction is unsafe and why recovery journals
must accompany a stopped database. This implementation preserves that recovery
state in its private copy while the source remains offline.

## Verification status

Windows unit/integration tests and a CLI round trip passed, including rejecting
backup while the server holds its lock and serving restored image/HFS bytes
after restart. `go test ./...` and `go vet ./...` pass. Linux code cross-compiles;
Linux execution and race checks run in CI. The local host has neither Docker
nor a CGO C compiler, so container and race execution were unavailable locally.
