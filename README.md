# EmbyMedia Operations

English | [中文](README.zh.md)

Private self-hosted media operations for Emby, CloudDrive2, 115, Hermes, and DeepSeek Harness.

This repository extends [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) with an EmbyMedia business package, migration utilities, hardened Debian deployment files, a browser operations console, and a full MCP adapter for Weixin-driven Hermes operations.

<a id="run"></a>

## Current deployment

The supported deployment runs on Debian 13. Emby, CloudDrive2, PostgreSQL, the DSH Web application, the standalone Go/Vue operations service, the HTTP login service, Caddy, and the Hermes messaging gateway run on one host. Caddy terminates public forwarding and login; service listeners remain host-local or container-private deployment details rather than general public ingress.

The repository does not contain production credentials. Runtime secrets live under `/etc/embymedia/secrets/` and service environment files under `/etc/embymedia/`.

## Capabilities

- Inventory Emby libraries, items, STRM files, users, tasks, plans, and audit records.
- Return exact item counts by Emby type, including `Movie`, `Series`, and `Episode`.
- Inspect continuing-series status and aired missing episodes.
- Search 115 resources, preserve protected-share credentials outside model-visible results, and inspect recursive leaf evidence.
- Create canonical plans for scans, resource onboarding, series repair, metadata changes, user policy changes, cleanup, deletion, and undo.
- Execute plans with write-mode enforcement, target revalidation, audit records, partial-state handling, and independent verification.
- Operate the plan-and-verify workflow from DSH, or use Hermes over Weixin with the standalone Go MCP registry for its registered 115, CloudDrive2, Emby, task, and system operations.

## Hermes and MCP

Hermes v0.21 connects to the standalone Go service over Streamable HTTP at `http://127.0.0.1:3080/mcp`. Its `embymedia` registry discovers eighteen tools. Discovery proves protocol registration; each call can still fail because of upstream configuration, permissions, or an explicitly unavailable provider action.

```sh
hermes mcp add embymedia --url http://127.0.0.1:3080/mcp
hermes mcp test embymedia
```

The Go service serves MCP, the Web UI, and REST on port 3080. `/agents` is the browser console, not an MCP transport address. Caddy authenticates public access to port 3080; same-host Hermes uses loopback and does not depend on the unpublished legacy SSE listener on port 3081.

Example Weixin requests:

```text
列出 Emby 媒体库和路径。
搜索名称中包含这部剧的 115 资源。
刷新 Emby 媒体库。
检查 CloudDrive2 挂载清单。
查询这个后台任务的状态和错误。
```

MCP mutation tools execute their configured provider action directly and return upstream errors. Share-link generation, CloudDrive2 remount, and running-task cancellation report explicit unavailable errors instead of synthetic success. Use the DSH plan path when an operation requires canonical `plan -> execute -> verify` controls.

## Repository layout

```text
packages/embymedia/operations/
packages/embymedia/preset/
packages/embymedia/ui/
apps/embymedia-migrate/
apps/embymedia-control-helper/
deploy/
migration/
```

The repository contains two application paths. The DSH packages own the canonical planning, execution, and verification workflow. The standalone Go service owns its Vue console, REST API, SQLite state, and eighteen-tool MCP registry; the deployed Hermes configuration uses this Go registry rather than the DSH dispatcher.

<a id="run-from-source"></a>

## Development

Prerequisites: Node.js 22.19 or newer, pnpm, and PostgreSQL for database-backed tests.

```sh
pnpm install
pnpm exec tsc -b packages/embymedia/operations/tsconfig.json
pnpm exec vitest run --root . packages/embymedia/operations/tests --no-file-parallelism
pnpm --filter @embymedia/dsh-operations bundle
```

The MCP deployment package is self-contained:

```sh
cd deploy/hermes/embymedia-mcp
npm ci --ignore-scripts
node --check src/index.js
```

## Deployment

Deployment files assume `/opt/embymedia/current` points at the active release and `/srv/embymedia/data` owns persistent data. Review every file under `deploy/` before applying it to another host; network names, paths, UIDs, and storage layout are deployment-specific.

After updating the operations Host:

```sh
pnpm exec tsc -b packages/embymedia/operations/tsconfig.json
pnpm --filter @embymedia/dsh-operations bundle
sudo systemctl restart embymedia-dsh.service
```

Hermes runs its messaging gateway as a user systemd service and discovers `http://127.0.0.1:3080/mcp` from `~/.hermes/config.yaml`. Only one Hermes gateway may poll a given Weixin iLink bot account.

## Safety

- Never commit `.env`, API keys, cookies, passwords, Weixin tokens, 115 access codes, or generated credential stores.
- Keep MCP on loopback for same-host clients, require Caddy authentication for public port 3080, and block DSH `/internal/*` routes at every reverse proxy.
- Keep the scheduler disabled unless its write-policy path is explicitly reviewed and enabled.
- Treat `previewed`, `queued`, `running`, and `verifying` as non-terminal states; only verified `done` is success.
- Preserve local encrypted backups and complete an isolated restore before destructive infrastructure changes.

## Upstream and license

DeepSeek Harness remains the upstream framework. Preserve its license and third-party notices when rebasing or redistributing this private derivative.

[MIT](LICENSE) — see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
