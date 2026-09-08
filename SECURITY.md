# Security and deployment boundaries

This is a pre-v1 application. See [release acceptance](docs/RELEASE.md) for
unverified behavior and [authentication](docs/AUTHENTICATION.md) for the exact
credential and HTTP contracts. No supported-version or security-response SLA
has been established.

## Exposure and credentials

A fresh instance has administration disabled, private HFS access unavailable,
and public uploads disabled. Image URLs, the UI, health, and server information
remain public. An unguessable image ID is not authorization; do not store
confidential images on a publicly reachable instance. Optional referer filtering
is configured with `KAVEN_ALLOWED_DOMAIN_NAMES`; it allows empty referers and
can be bypassed by clients that forge the header. It is not an access-control
boundary. Public HFS roots permit unauthenticated reads, while their
writes still require administration.

The supplied Compose file publishes port 5558 on the host's interfaces. Restrict
the deployment to a trusted network, or terminate HTTPS at a reverse proxy and
prevent clients from bypassing it to reach the backend. For a proxy running on
the same host, change the existing port mapping to `127.0.0.1:5558:5558`.
For a proxy in another container, use a private container network and configure
reachability for that topology. The server itself serves HTTP.

Preserve the public request Host and request path through the proxy. Digest
authentication signs the method and request target, and unsafe authenticated
requests with an Origin header are checked against Host. Forwarded headers do
not establish trusted client addresses; audit records contain the direct peer,
which may be the proxy. Configure rate, body-size, timeout, and resource limits
at the deployment boundary to suit the intended upload sizes.

Use one strong, unique administrative password via `KAVEN_ADMIN_PASSWORD_FILE`;
the [secret-mount example](docs/AUTHENTICATION.md#configuration-contract)
documents the supported configuration. The image's unprivileged user must be
able to read the secret. Keep it outside `/data` and outside the repository.
Never bake secrets into images or include them in build arguments. Rotate by
replacing the secret and restarting the service. All old Digest nonces expire
on restart. There is one administrative role, not per-user or per-file ACLs.

## Files and resource use

Only the application account and trusted operators should write to the data
directory. Path validation, symlink rejection, and atomic publication protect
HTTP operations; they do not make a directory writable by hostile local users
a supported deployment. Use one process with one local volume, not replicas
or shared network storage. Run with the image's default unprivileged user and
mount only the directories it needs.

Upload, image-dimension, multipart, and concurrency limits are implemented;
see [development configuration](docs/DEVELOPMENT.md). There is no storage quota,
automatic cache-retention policy, malware scanner, or application-wide request
rate limiter. Public uploads permit disk consumption by unauthenticated users.
Monitor free space and set host/container resource budgets. Native codecs parse
untrusted bytes; rebuild and verify images when their dependencies change.

HFS hosts arbitrary file bytes. Do not treat content as trusted merely because
it was accepted or served by this application. Audit queues can drop events
under load and are not a complete security audit trail. Logs, request URLs,
user-agent strings, filenames, and stored metadata can contain personal or
sensitive data; restrict access and set a retention policy outside the service.

## Backup and restore

Backups contain media, database metadata, and audit records. The manifest checks
integrity but is neither a signature nor encryption. Protect backup access and
copies like the live data, and retain configuration and secrets separately.
Only restore trusted snapshots. Follow [offline backup](docs/BACKUP.md) and
[upgrade/rollback](docs/UPGRADE.md); do not copy a live SQLite database file alone.

## Reporting a vulnerability

Do not put credentials, private media, database dumps, or exploit details in a
public issue. Use the repository host's private vulnerability-reporting facility
if it is enabled, or an established private contact with the maintainer. This
repository does not currently specify a dedicated security contact or promise
that private reporting is enabled. A public request for a private contact
should contain no vulnerability details.

Privately include the affected commit/image ID, architecture, relevant
configuration with secrets removed, a minimal synthetic reproduction, and
the observed impact. Preserve evidence without continuing to expose an
affected installation; restrict access and rotate any exposed credentials.
