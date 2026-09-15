# Database backup and restore

Kaven Media Server backups contain the SQLite database only. Media, cache,
Bing, HFS, temporary, and private backup directories are never copied or
restored. This keeps snapshots small and lets operators manage large file trees
with normal host storage, mounts, replication, or other backup tools.

The database still contains paths and HFS virtual-root settings. Those records
do not make the referenced files part of the snapshot. Before using a restored
database, place or mount the required directories on the destination host and
review every mapping.

## Settings page

The authenticated **Settings > Backups** page stores multiple snapshots under
`<data-dir>/backup/`. This directory is private and is not exposed through HFS.
Each snapshot is a direct child whose name uses letters, numbers, dots,
underscores, or hyphens and directly contains `manifest.json` and `data/`.

Creating a snapshot briefly restarts the application so SQLite and background
writers are closed. The resulting format 3 snapshot contains only:

```text
snapshot-name/
├── manifest.json
└── data/
    └── kaven-media.db
```

Restore uses a guided flow. It shows the managed directories that will remain
untouched, automatically reads the directory tree below `/data`, lets the
administrator select detected directories for HFS virtual roots, and requires
final confirmation. Symlinks are omitted from the bounded directory inventory.
The selected backup's virtual roots are shown exactly as stored. HFS is a
virtual route namespace rather than a required physical directory. Review and
manually configure every mapping as an absolute server path; `readOnly`
controls writes independently.

The server validates the selected database, manifest checksum, producer
version, schema, restored application settings, and selected HFS mappings before scheduling
the restart. Web restore keeps the current administrator credential and
sessions, retains other application settings from the snapshot, applies the HFS
mappings chosen in the wizard, swaps only SQLite files,
and leaves `upload/`, `cache/`, `download/bing/`, `tmp/`, `backup/`, and every configured
virtual-root target intact.
Keep the page open until it reconnects.

## Command-line backup

Stop the server first. `serve`, `check`, and `backup` share an OS-level
data-directory lock, and contention fails promptly.

```sh
kaven-media backup --data-dir ./data --output ./backups/db-2026-09-10
```

The output directory must not exist. Its parent must exist. A direct child of
`<data-dir>/backup/` is supported; that repository is not included in the
snapshot. Backup copies SQLite and any recovery journal to private staging,
consolidates the staged database, validates it, writes a SHA-256 manifest, and
publishes the directory atomically. Source SQLite bytes are unchanged.

The default byte limit is 1 TiB and can be changed with `--max-bytes N`.
Capacity preflight requires the database bytes plus a 64 MiB operational
reserve. A successful command prints JSON containing the path, file count,
bytes, manifest version, creation time, and producer identity.

Database backup validates SQLite integrity, foreign keys, known migration
history, and the current schema. It intentionally does not require referenced
image or HFS files to exist because those trees have a separate lifecycle.

## Command-line restore

Restore publishes only to a new, absent data directory:

```sh
kaven-media restore --input ./backups/db-2026-09-10 --data-dir ./restored-data
kaven-media check --data-dir ./restored-data
```

Restore verifies the manifest, database size and SHA-256 checksum, exact known
producer version, migration history, schema, SQLite integrity, and foreign
keys. A known version mismatch is rejected before copying.

The new data directory contains the restored `kaven-media.db` and empty core
mount points using the database's configured upload and download directories
(defaults: `upload/` and `download/bing/`), plus `cache/` and `tmp/`. Before starting
the service:

1. Copy, mount, or otherwise expose the image, Bing, and HFS trees expected by
   the database.
2. Ensure their ownership and permissions match the server account.
3. Recreate every external HFS mount at its configured absolute path; relative
   HFS mappings resolve below the restored data directory. Apply the intended
   writable or read-only permissions.
4. Start the service on an isolated address, sign in, and use **Settings** to
   review or replace every HFS virtual-root mapping.
5. Exercise representative image IDs and HFS paths before cutover.

`check` reports missing managed files; it does not recreate them. Keep the old
installation, its media directories, and the database snapshot until the
restored deployment has been verified.

## Docker examples

```sh
docker compose stop kaven-media
docker compose run --rm --no-deps \
  -v "$PWD/backups:/backups" \
  kaven-media backup --data-dir /data --output /backups/db-2026-09-10
docker compose start kaven-media
```

For a rehearsal restore, create a fresh host directory and mount any separately
preserved file trees at the desired locations:

```sh
mkdir -p restore-work
docker run --rm \
  --mount type=bind,src="$PWD/restore-work",dst=/restore \
  --mount type=bind,src="$PWD/backups",dst=/backups,readonly \
  kaven-media-server:your-version restore \
  --input /backups/db-2026-09-10 --data-dir /restore/data
```

When starting the restored container, bind `restore-work/data` to `/data` and
add the media/HFS mounts selected for the new deployment. Only one application
process may use the SQLite data directory.

## Format and recovery

Format 3 manifests contain exactly one file entry, `kaven-media.db`, and the
producer version, revision, modified-tree flag, and Go version. Earlier formats
are not accepted. Manifest paths use forward slashes, times use UTC RFC 3339
fractional-second precision, and files record size, lowercase SHA-256, and
modification time. The manifest is checksummed metadata, not a signature or
encryption; protect snapshots like the original database.

Interrupted operations remove owned staging where possible. A Web restore that
was validated and recorded before a process or host interruption resumes its
database-only swap before SQLite is reopened. Never delete the lock file to
bypass a running process.
