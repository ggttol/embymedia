# EmbyMedia operations

English | [中文](operations.zh.md)

## Summary

Operate the standalone Debian media service while preserving its database, browser identities, provider credentials, media, and recovery path. Installation and recovery require explicit operator authorization; source cleanup does not grant it.

## Table of Contents

- [Deployment](#deployment)
- [NAS transfer worker](#nas-transfer-worker)
- [Access](#access)
- [Backup and isolated restore](#backup-and-isolated-restore)
- [Media and mount safety](#media-and-mount-safety)

## Deployment

The supported release layout is `/opt/embymedia-v2/current/bin/embymedia` with installed deployment files below the same release root. `embymedia-v2.service` owns `/srv/embymedia/data/embymedia.db` and loopback HTTP port 3080. Caddy and the HTTP login service protect public access; Compose manages Emby and CloudDrive2. Provisioning and image configuration are owned by [`deploy/scripts`](../deploy/scripts) and [`deploy/compose.yml`](../deploy/compose.yml).

Initial secret setup runs as root after provisioning and takes an artifact directory whose `manifest.json` contains `imageIds.clouddrive2` and `imageIds.emby`. Supply immutable `repository@sha256:<digest>` image references available to Docker; `init-secrets.sh` reads this manifest but does not fetch images. Existing V2 upgrades use the configured stack and do not require rerunning initial secret setup.

After a backup and release review, build on the source checkout with `make install-web` and `make build-linux`. Transfer the release source plus both Linux binaries and their `.sha256` files from `bin/` to the provisioned Debian host. From that release source directory, an authorized operator can install a unique release ID:

```sh
sudo deploy/scripts/install-release.sh "$PWD" RELEASE_ID
```

Replace `RELEASE_ID` with a new non-existing release name. The installer verifies the artifact, checks the database, and switches `current` atomically. It waits up to one hour for running background work before stopping V2; a drain timeout leaves the current release active. `EMBYMEDIA_DEPLOY_FORCE=1` cancels that work instead and is only for explicitly accepted interruption. Pending work remains durable; interrupted effectful attempts require review before retry.

Release installation preserves the webhook enablement choice, browser login, generated STRM root, and rollback-tracked configuration. The shared `/etc/caddy/Caddyfile` and other applications' route fragments are host-owned; EmbyMedia releases update only `/etc/caddy/snippets/embymedia.caddy`, `/etc/caddy/routes/embymedia.caddy`, and `/etc/caddy/sites/embymedia.caddy`, validate the complete configuration, then reload Caddy. The systemd override prevents environment enumeration in logs. Artifact build, source cleanup, and CI do not perform deployment.

Quark-to-115 imports save into the selected Quark directory, then spool one file at a time before verified upload to the default 115 account's exact `/emby/_待整理` directory. `transfer_temp_dir` defaults to `/srv/embymedia/data/transfers`; `transfer_min_free_bytes` defaults to 10 GiB and is required in addition to the current file size. `quark_download_connections` sets the parallel ranged connections per file (default 4, allowed 1-8); four measured about 3.4x a single stream, and eight measured lower than four. A configured 115 Open Platform token enables native rapid or multipart upload. Without that token, set `clouddrive_c115_account_id` to the matching managed account: the writable CloudDrive2 mount must expose `/115open/emby`, and the importer writes a task-owned staging file, publishes it after `fsync`, then verifies the final parent, name, size, and SHA-1 through 115 before deleting the spool. The CloudDrive2 FUSE mount reports directories as root-owned without propagating default ACLs, so `ensure-transfer-access.sh` grants the service user access on `_待整理`, `电视剧追更`, and `综艺追更`; the `embymedia-v2` unit runs it before start and the CloudDrive monitor re-runs it every minute so directories created later become writable without a restart. A plain import publishes flat into `_待整理` rather than mirroring the Quark share's folder names, because a subdirectory created through the provider API is never service-writable. Keep the spool on persistent private storage so interrupted work can reconcile after restart.

## NAS transfer worker

The optional NAS worker keeps Debian as the only production database and task owner while moving Quark download bytes and CloudDrive2-backed 115 writes to a direct-connected NAS. Each instance uses an independent CloudDrive2 `/Config`, mount root, cache, and admin port; the NAS mount maps the same managed 115 account's `/115open/emby` directory to `/CloudNAS/CloudDrive`. The worker requires the shared `.embymedia-health-canary`, the exact `_待整理/.embymedia-nas-worker-health-canary` identity visible through both CloudDrive2 and the 115 API, writable destination directories, a persistent private spool, and at least the configured free-space reserve plus the current file size. It never falls back to an HTTP or SOCKS proxy.

On Debian, supply a previously verified OpenSSH `known_hosts` entry instead of trusting a network scan during deployment. The controller setup creates a dedicated Ed25519 key owned by `embymedia`, records strict host-key checking, and adds the four `EMBYMEDIA_NAS_WORKER_SSH_*` variables to `/etc/embymedia/v2.env`:

```sh
sudo deploy/scripts/configure-nas-worker-controller.sh gaotao@NAS_HOST 5022 VERIFIED_KNOWN_HOSTS
```

Transfer the worker binary, checksum, deployment files, generated public key, and no private credential to the NAS. Use an authorized NAS administrator once to install the root-owned, path-confined access helper; run the worker installer as the restricted NAS account; then start the digest-pinned CloudDrive2 Compose stack and configure its 115 mount through the loopback or protected-LAN Web UI:

```sh
sudo deploy/scripts/install-nas-worker-root.sh RELEASE_TREE /volume1/docker/clouddrive2/CloudNAS/CloudDrive gaotao 1026 100
deploy/scripts/install-nas-worker.sh RELEASE_TREE CONTROLLER_PUBLIC_KEY
CLOUDDRIVE_IMAGE=repository@sha256:digest docker compose -f /volume1/homes/gaotao/embymedia-transfer/clouddrive-compose.yml up -d --wait
```

The installer replaces only the `embymedia-nas-worker` entry in `~/.ssh/authorized_keys`; that key cannot allocate a PTY, forward ports, use an agent, or execute an arbitrary command. Its only privilege is one exact passwordless helper with no command arguments: the worker passes a validated relative destination over stdin, and the root-owned helper confines ownership changes to one existing directory below the configured mount. Quark credentials cross only encrypted stdin for the download operation and never enter arguments, NAS configuration, progress output, or logs. A transfer keeps its range checkpoint and spool on the NAS, hashes the complete file before publication, writes the final destination name directly through the NAS CloudDrive2 mount, and waits for Debian to verify the exact 115 parent, name, size, and SHA-1 before a separate commit request removes the spool. Never rename a CloudDrive2 file whose upload is still in flight: 115 then keeps a `<name>**..uploading` object that never settles. When such a placeholder exists without a settled object, the worker rewrites the destination, because the mount still reports the stale file at its full size. SSH loss, cancellation, mount failure, an existing foreign destination, or ambiguous provider state fails visibly and leaves resumable evidence.

## Access

Create separately named Agent tokens in `/agent`; retain the one-time plaintext secret securely. Clients connect to `http://127.0.0.1:3080/mcp` from the host with `Authorization: Bearer <token>` or `X-Agent-Token: <token>`. For remote access, use the authenticated deployment endpoint and an appropriate protected network path. Plain HTTP does not encrypt passwords or tokens in transit.

Caddy strips supplied browser identity headers on public Agent routes. Browser login and Agent scope are distinct authorities; Agent tokens cannot approve deletion requests or enable the dangerous-action switch. Native Emby deletion requires the user's authorized Emby administrator device session. See [safety](../SAFETY.md).

Use the running HTTP endpoint for clients sharing production state. Never start a stdio or database-check process against the active database: the exclusive owner lock prevents competing task recovery, migrations, and schedulers. An isolated `-check-db` can migrate its copy; it is not a read-only database inspection tool.

## Backup and isolated restore

The installed backup job leaves V2 and the Compose stack running. It stages SQLite databases through the online backup API, checks them with `PRAGMA quick_check`, retries changing regular files, and omits WAL/SHM companions of copied databases. Restic receives a private staged tree containing the V2 database, browser-user database, Emby and CloudDrive configuration, and generated `/srv/embymedia/data/strm-v2` catalog.

An authorized operator can run the installed backup and restore a chosen snapshot into a new isolated directory:

```sh
sudo /opt/embymedia-v2/current/deploy/scripts/backup.sh
sudo /opt/embymedia-v2/current/deploy/scripts/restore-isolated.sh SNAPSHOT_ID /srv/embymedia/restore-DRILL_ID
```

Replace both IDs; the restore directory must not already exist. The restore recognizes direct-path and staged snapshots, validates restored browser identities, ensures the generated STRM directory exists, and invokes the current binary's isolated database check. It does not activate the restored tree. Preserve an untouched snapshot because validation can migrate the restored database.

Do not copy live SQLite files by hand or replace production state with an unvalidated restore. Store Restic recovery passwords and provider credentials securely outside the source repository; the listed backup inputs do not include `/etc/embymedia/secrets` or original media. Retention keeps fourteen daily snapshots, so a successful local backup is not an off-host disaster-recovery guarantee.

## Media and mount safety

Emby's shared read/write access to generated STRM directories depends on the ACL policy in [`configure-strm-access.sh`](../deploy/scripts/configure-strm-access.sh). Preserve it during install, restore, and directory creation. Never broaden permissions on original media as a substitute for fixing generated-directory access.

The CloudDrive recovery timer observes container health and the mount canary, then performs bounded recovery under the deployment/backup lock with a fifteen-minute cooldown. Recovery can interrupt active media work; inspect task and provider state afterward. A mount table entry alone does not prove a usable FUSE filesystem.

Normalization and library cutover scripts are explicit maintenance tools, not startup hooks. Their frozen inventories, ID-bound plans, staged review/recycle roots, and replacement-library checks protect against deleting an unverified copy. Do not run them merely to validate a source checkout. The [normalization decision](../.agents/notes/implemented/feature/2026-09-09-canonical-media-library-normalization.md) records the trade-offs.
