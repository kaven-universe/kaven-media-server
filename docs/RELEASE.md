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
| Compatibility | Representative legacy requests, response shapes, headers, file bytes, and deliberate differences reviewed | Contract tests exist; final parity acceptance pending |
| Image codecs | Representative real images compared with legacy output | Generated codec matrix and captured animated-GIF behavior exist; real deployment corpus pending |
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
it does not replace the compatibility, codec-corpus, or recovery
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
Review [compatibility differences](COMPATIBILITY.md) with the intended client
workflows; close gaps or explicitly document and accept them before declaring v1.

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
one multi-architecture manifest without rebuilding under emulation. Prerelease
tags publish only their complete version. Stable tags additionally publish
major, major/minor, and `latest` aliases. The workflow verifies the published
image index, extracts Alpine Linux AMD64 and ARM64 binaries, bundles the
verified CI evidence, writes SHA-256 checksums, and creates a GitHub Release
with generated notes. Version-specific `-amd64` and `-arm64` tags retain the
manifest's accepted platform images.

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
GitHub `main` commit, and creates a synthetic commit containing the exact current
tree. Its only parent is the previous synthetic public snapshot; the first
snapshot has no parent. The private source commit is printed for local records
but is not written into the public commit. Publish only after reviewing the
reported destination and commit identities:

```powershell
./scripts/publish-public-release.ps1 -Tag v1.0.0-rc.1 -Push
```

For the first release, the script publishes GitHub `main` before its tag so
GitHub can discover the tag-triggered workflow from the new default branch.
Later releases update `main` and create the tag atomically. The tag starts the
GitHub release workflow. The script does not change the private branch, push to
`origin`, or create a local release tag. Run it from the private repository for
every release; never merge GitHub `main` back into private `main`.

### Migrated deployment backup

Format 2 backups bind their producer revision to the restoring executable. A
backup made by a private-history build therefore cannot be restored directly by
the synthetic public release commit, even when both commits contain the same
source tree. After the tagged workflow publishes the image, regenerate the
portable deployment backup from the already restored data using that exact
image:

```powershell
docker run --rm `
  -v "C:\path\to\restored-data:/data" `
  -v "C:\path\to\backups:/backups" `
  kaven-universe/kaven-media-server:1.0.0-rc.1 `
  backup --data-dir /data --output /backups/kaven-media-backup-v1.0.0-rc.1
```

Use the actual Docker Hub image configured for the workflow. Keep the existing
private backup until the new snapshot restores successfully with the public
image and `check` reports a healthy destination.
