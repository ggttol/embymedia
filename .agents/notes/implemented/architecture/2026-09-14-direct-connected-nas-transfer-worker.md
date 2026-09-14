# Agent Note: Direct-connected NAS transfer worker

Status: implemented

English | [中文](2026-09-14-direct-connected-nas-transfer-worker.zh.md)

## Problem

The production Debian host reaches some Quark CDN node pools but silently loses connections to others: one signed download address family fails at TCP connect while a different pool completes in the same second. The single-owner server therefore owns a data plane it cannot reliably reach, and routing bulk bytes through an HTTP or SOCKS proxy spends a metered tunnel to work around a routing fault.

Quark-to-115 imports also coupled download bytes to the database owner: spool, segment checkpoints, and the CloudDrive2 mount that publishes into 115 all lived on that host. A blocked CDN region stalled unrelated media work, and an operator could not point the transfer at a machine with working connectivity.

## Decision

A forced-command SSH worker moves only the Quark-to-115 data plane to a direct-connected NAS. Debian remains the single production database and task owner: it selects resources, validates provider identities, persists durable task state, cancels work, and performs the final 115 verification. One bounded JSON request per operation crosses encrypted stdin, and the worker is a stateless invocation that exits after emitting newline-delimited events.

`internal/transfer` owns the shared download, checkpoint, and publish mechanics. Downloads keep four ranged connections with a 10 MiB segment ceiling, and a segment checkpoint is persisted only after its bytes are durable, so a restart reuses completed work instead of re-downloading. Publication writes the final destination name directly, because renaming a FUSE file while CloudDrive2 is still uploading it leaves 115 with a permanent `<name>**..uploading` object, and content identity is confirmed through the 115 API instead of reading the file back. The worker's HTTP transport never consults environment proxies.

The NAS runs an independent CloudDrive2 instance with its own `/Config`, mount root, cache, and admin port, plus a private spool and a fixed `_待整理/.embymedia-nas-worker-health-canary`. Debian resolves that canary through the 115 API before an import is accepted, which binds both the NAS mount and the controller to the configured 115 account; a mismatch fails before any provider write. Background spool bytes are removed only after Debian verifies the exact 115 parent, name, size, and SHA-1 and then sends a separate commit request.

## Alternatives considered

**Publish a Quark-only proxy route.** Rejected because it still spends a metered tunnel on every transferred byte and leaves the database owner responsible for the data plane.

**Front the NAS with OpenList, WebDAV, or another mount.** Rejected because an extra component does not change the CDN path, and the existing segmented download already matches measured Quark behaviour.

**Share one CloudDrive2 instance, mount, or spool between the two hosts.** Rejected because separate FUSE mounts, caches, and admin ports keep either host's recovery from disturbing the other, and two writers publishing one destination name would be unverifiable.

**Let the worker decide completion.** Rejected because only the controller holds the 115 credential and the task record; a worker reporting its own success could not prove the destination object's identity.

## Consequences

The controller survives a blocked CDN region, because the data plane no longer depends on its own egress. Controller installation stays atomic, and NAS installation is idempotent: the installer replaces one `authorized_keys` entry, and one root-owned helper with no command arguments, reachable through a single passwordless sudo rule, may only chown and chmod one existing directory below the configured mount, resolved with `O_DIRECTORY | O_NOFOLLOW`.

Two operational couplings remain. Transfers require the NAS CloudDrive2 mount and canary, so a NAS-side outage fails the import visibly and preserves resumable evidence instead of falling back to a proxy. Quark credentials cross only encrypted stdin for a download operation, so they stay absent from process arguments, NAS configuration, progress output, and logs.
