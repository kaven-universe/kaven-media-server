# Release acceptance

The implementation is pre-v1. Documentation and CI configuration do not by
themselves establish release readiness. Pushing a semantic version tag starts
the release workflow after its embedded acceptance workflow succeeds.

## Operator guides

- [Backup and restore](BACKUP.md)
- [Upgrade and rollback](UPGRADE.md)
- [Security and deployment boundaries](../SECURITY.md)
- [Credentials and rotation](AUTHENTICATION.md)

## Required evidence

For the exact proposed source commit, retain the following with the release:

| Area | Required evidence | Current status |
| --- | --- | --- |
| Go quality | Formatting, all tests, race detector, and vet pass | Local tests/vet passed; race execution requires CI |
| Container | Native AMD64 and ARM64 jobs pass build, runtime codecs, architecture, and HTTP smoke checks | Configured; successful execution pending |
| API | Current request and response contracts covered by integration tests | Local contract tests pass |
| Image codecs | Representative real images processed by the production runtime | Generated codec matrix exists; real deployment corpus pending |
| Recovery | Restore and restart with matching build; rehearse candidate upgrade and rollback on disposable data | Windows CLI round trip and manifest identity enforcement pass; container upgrade/rollback rehearsal pending |

Record test commands, platform, outcomes, CI run references, image IDs, and
`kaven-media version` output, and remaining limitations. Confirm its revision
matches the OCI `org.opencontainers.image.revision` label. Do not include secrets or unsanitized deployment data.
Use `kaven-media check --json` when retaining machine-readable integrity
evidence; archive the report even when its nonzero exit indicates issues. Its
`execution.producer.revision` must match the candidate commit, and its ordered
`schemaVersions` must match the candidate's embedded migration history. Review
its destination `tableCounts` against the accepted data set. Require
`checksumsChecked` to equal the image table count and retain the reported
`checksumBytes` total.
Successful CI runs produce `ci-evidence-<commit>` for 90 days. It contains
quality, AMD64, and ARM64 JSON records, a run manifest, and `SHA256SUMS`.
The assembly job verifies these files and adds `verification.json` before
upload.
Download and retain that artifact with the release because CI retention is not
permanent. Its presence proves every prerequisite CI job completed successfully;
it does not replace the codec-corpus or recovery
evidence gathered outside CI.
After extraction, verify the bundle against the proposed commit and retain the
result:

```sh
kaven-media verify-ci \
  --evidence-dir ./ci-evidence \
  --revision 0123456789abcdef0123456789abcdef01234567
```

The [roadmap](ROADMAP.md) tracks outstanding acceptance work.

## Candidate validation

Run the quality commands in [development](DEVELOPMENT.md#common-commands) and
both native container jobs for the candidate commit. Execute backup, restore,
upgrade, and rollback acceptance checks on isolated data. Check that restore
uses the matching schema version and that rollback uses the preserved old data.
Review intended client workflows before declaring v1.

When publishing is separately authorized, tag the reviewed commit and identify
the exact images and supported architectures. Include configuration changes,
schema/data changes, upgrade and rollback instructions, validation evidence,
and known limitations in release notes. Preserve the matching previous build
for operators who need to restore old backups.

## Automated publication

Configure these GitHub repository secrets before pushing a release tag:

- `DOCKERHUB_USER`: Docker Hub account or organization member;
- `DOCKERHUB_TOKEN`: Docker Hub access token with permission to push.

The optional repository variable `DOCKERHUB_IMAGE` overrides the default
`<DOCKERHUB_USER>/kaven-media-server` image name. It must use lowercase
`namespace/repository` syntax.

Push a SemVer tag such as `v1.0.0-rc.1` or `v1.0.0`. The workflow reruns the
quality and native AMD64/ARM64 container acceptance jobs. Each native job
exports its exact accepted image; the publisher pushes those images and creates
one multi-architecture manifest without rebuilding under emulation. Every
release publishes `latest` and a major-version alias derived from the release
version (`1.7.3` publishes `1`, for example). The workflow verifies the
published image index, extracts Alpine Linux AMD64 and ARM64 binaries, bundles the
verified CI evidence, writes SHA-256 checksums, and creates a GitHub Release
with generated notes. Version-specific `-amd64` and `-arm64` staging tags retain
the manifest's accepted platform images.

Never move or reuse a published version tag. Docker image digests and the commit
revision in `docker-image.json` identify the immutable release inputs.

### Public snapshot history

The authoritative repository may retain its complete private development
history while GitHub receives one source snapshot per release. Configure the
public repository once:

```powershell
git remote add github git@github.com:kaven-universe/kaven-media-server.git
```

Start with an empty GitHub repository. Do not initialize its default branch or
push a branch descended from the private history. Commit all intended private
changes, verify that the working tree is clean, and preview publication:

```powershell
./scripts/publish-public-release.ps1 -Tag v1.0.0-rc.1
```

The publisher audits tracked filenames, rejects submodules, reads the previous
GitHub `main` commit, and creates a parentless synthetic commit containing the
exact current tree. Every release is therefore a self-contained public
snapshot rather than an accumulating public branch history. The private source
commit is printed for local records but is not written into the public commit.
Publish only after reviewing the reported destination and commit identities:

```powershell
./scripts/publish-public-release.ps1 -Tag v1.0.0-rc.1 -Push
```

For the first release, the script publishes GitHub `main` before its tag so
GitHub can discover the tag-triggered workflow from the new default branch.
Later releases replace `main` with a lease-protected force update and create the
tag atomically. The tag starts the GitHub release workflow. The script does not
change the private branch, push to `origin`, or create a local release tag. Run
it from the private repository for every release; never merge GitHub `main`
back into private `main`.

After an RC workflow successfully creates its GitHub Release, its final step
uses the workflow token's `contents: write` and `actions: write` permissions to
delete every older semantic RC tag and completed RC Release workflow runs older
than the newest three. Cleanup deliberately occurs after successful publication
so a failed candidate cannot remove the last usable RC tag. Active runs, non-RC
tags, non-RC workflow runs, and existing GitHub Release records are not deleted.
Because each public snapshot is parentless, deleting an old RC tag also leaves
its commit unreachable by Git refs. GitHub controls the eventual garbage
collection of those unreachable objects; a raw commit URL may continue to work
for some time after its tag and branch references are gone. Stable-release
commits remain reachable through their retained version tags.

### Migrated deployment backup

Format 3 backups retain their producer revision for provenance and require the
same known application version during restore. Regenerating a deployment backup
from the published image remains useful for release records:

```powershell
docker run --rm `
  --mount "type=bind,source=C:\path\to\restored-data,target=/data" `
  --mount "type=bind,source=C:\path\to\backups,target=/backups" `
  kaven-universe/kaven-media-server:1.0.0-rc.1 `
  backup --data-dir /data --output /backups/kaven-media-backup-v1.0.0-rc.1
```

Use the actual Docker Hub image configured for the workflow. Format 3 contains
only SQLite; preserve the managed file trees independently and map them into the
rehearsal deployment. Keep the existing backup until the snapshot restores
successfully and `check` reports a healthy destination.
