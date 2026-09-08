# Roadmap

The order prioritizes a stable contract and storage core before feature volume.
Checkboxes describe implementation status, not merely file presence.

## Phase 0: foundation

- [x] Initialize standalone Go repository.
- [x] Add graceful HTTP server lifecycle.
- [x] Add SQLite bootstrap and persistent data directories.
- [x] Add Dockerfile and Compose bind-mount interface.
- [x] Add health and initial server-info endpoints.
- [x] Add embedded frontend hook.
- [x] Replace the bootstrap schema with numbered embedded migrations.
- [x] Add baseline configuration, database, HTTP, and shutdown tests.
- [x] Add CI for formatting, tests, race tests, vet, and container build.

## Phase 1: contracts and storage

- [x] Capture legacy API fixtures and representative image/filesystem fixtures.
- [x] Define Go DTOs for server info, upload responses, image metadata, and HFS
      entries.
- [x] Implement repositories for images, caches, Bing images, access records,
      and download records.
- [x] Implement safe path resolution and atomic file writes.
- [x] Implement `check` for database integrity and missing/orphaned files.

## Phase 2: image hosting

- [x] Implement image upload with limits, magic-byte validation, UUID, SHA-1,
      duplicate handling, and rollback cleanup.
- [x] Implement lookup by legacy ObjectId, UUID, and SHA-1.
- [x] Implement original image delivery with correct headers and range behavior.
- [x] Integrate libvips for metadata, resize, quality, and output formats.
- [x] Implement canonical derivative keys and concurrency-safe caching.
- [x] Record image access without delaying or breaking successful delivery.
- [x] Implement the protected legacy image collection endpoint with a 100-row cap.

## Phase 3: HFS and authentication

- [x] Decide and document secure first-run credential behavior.
- [x] Implement protected-route authentication and authorization tests.
- [x] Capture legacy image referer decisions and implement an optional bounded
      domain allowlist with malformed-header and route coverage.
- [x] Implement virtual-root listing and public/private roots.
- [x] Implement safe directory listing and file rendering/download.
- [x] Implement directory creation and bounded multipart upload.
- [x] Record completed downloads.
- [x] Test encoded traversal, symlink escape, race, overwrite, and partial upload
      cases.

## Phase 4: Bing archive and jobs

- [x] Implement the Bing metadata client with timeouts and retry policy.
- [x] Implement idempotent download and database synchronization.
- [x] Implement random Bing image delivery.
- [x] Add shutdown-aware scheduled jobs with overlap prevention.
- [x] Implement protected manual synchronization endpoints.

## Phase 5: frontend and release

- [x] Run the existing Vue UI unchanged against the compatibility API.
- [x] Fix same-origin API configuration and update product branding in the owned
      `web/` frontend source.
- [x] Automate frontend build and Go embedding in the Docker build (local tagged
      build verified; full container execution requires Docker/CI verification).
- [x] Add offline backup and verified restore commands/documentation.
- [x] Add an authenticated backup-folder restore UI with validated staging and
      an internal application restart for Docker deployments.
- [x] Add AMD64/ARM64 container build targets and runtime codec verification gates.
- [x] Capture legacy animated-GIF transformation behavior and enforce it in the
      production runtime codec matrix.
- [ ] Verify both native container CI jobs pass before release (local Docker and
      native codec execution are unavailable in the current development environment).
- [x] Add operator guides for backup, restore, upgrade, rollback, and security,
      with explicit release acceptance requirements.
- [x] Reject incompatible recorded SQLite migration history before journal or
      schema changes, with database-preservation regression tests.
- [x] Embed version/revision identity in release binaries and OCI labels, expose
      it through a machine-readable command, and verify CI image identity.
- [x] Add bounded machine-readable integrity reports for release and recovery
      evidence while preserving complete issue totals and failing exits.
- [x] Retain checksummed CI evidence for quality and successful native AMD64 and
      ARM64 container acceptance on the exact candidate commit.
- [x] Embed producer build identity in backup manifests and reject known restore
      mismatches while retaining format 1 restore compatibility.
- [x] Preflight backup and restore destination capacity before creating staging
      content, with a documented operational reserve.
- [x] Attach candidate build identity to machine-readable integrity evidence on
      healthy and unhealthy outcomes.
- [x] Record the ordered applied SQLite migration versions in machine-readable
      integrity evidence after candidate migrations are applied.
- [x] Record destination table counts in machine-readable integrity evidence
      for release and recovery comparisons.
- [x] Add a bounded verifier for downloaded CI checksums, candidate identity,
      quality gates, and native AMD64/ARM64 container evidence.
- [x] Configure CI to verify assembled evidence before artifact upload and
      retain its result inside the combined candidate artifact.
- [x] Recompute stored size and SHA-1 for every referenced original image during
      integrity checks and expose bounded machine-readable totals.
- [x] Route preserved mixed-case and 36-character hyphenated UUIDs from restored
      data while retaining lowercase alias fallback.
- [ ] Declare v1 only after parity and native container acceptance tests pass.

## Deferred possibilities

- Optional PostgreSQL and object-storage backends for multi-replica deployment.
- OpenAPI specification and generated TypeScript client.
- Content-addressed storage using a modern digest while retaining SHA-1 lookup.
- Administrative UI for jobs, storage integrity, and backup status.
