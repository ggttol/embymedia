# EmbyMedia Operations

English | [中文](README.zh.md)

Private self-hosted media operations for Emby, CloudDrive2, 115, Hermes, and DeepSeek Harness.

This repository extends [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) with an EmbyMedia business package, migration utilities, hardened Debian deployment files, a browser operations console, and a full MCP adapter for Weixin-driven Hermes operations.

<a id="run"></a>

## Current deployment

The supported deployment runs on Debian 13. Emby, CloudDrive2, PostgreSQL, the DSH Web application, the HTTP login service, Caddy, and the Hermes messaging gateway run on one host. Public forwarding terminates at Caddy; DSH, Emby, CloudDrive2, PostgreSQL, and the EmbyMedia MCP application route listen only on loopback or container-private addresses.

The repository does not contain production credentials. Runtime secrets live under `/etc/embymedia/secrets/` and service environment files under `/etc/embymedia/`.

## Capabilities

- Inventory Emby libraries, items, STRM files, users, tasks, plans, and audit records.
- Return exact item counts by Emby type, including `Movie`, `Series`, and `Episode`.
- Inspect continuing-series status and aired missing episodes.
- Search 115 resources, preserve protected-share credentials outside model-visible results, and inspect recursive leaf evidence.
- Create canonical plans for scans, resource onboarding, series repair, metadata changes, user policy changes, cleanup, deletion, and undo.
- Execute plans with write-mode enforcement, target revalidation, audit records, partial-state handling, and independent verification.
- Operate from either the DSH browser or Hermes over Weixin with the same business dispatcher and structured results.

## Hermes and MCP

Hermes connects to the local `embymedia` MCP server over stdio. The adapter exposes the same thirteen wire tools as DSH:

```text
embymedia_health      embymedia_library     embymedia_resource
embymedia_series      embymedia_analyze     embymedia_task
embymedia_audit       embymedia_user        embymedia_schedule
embymedia_config      embymedia_plan        embymedia_execute
embymedia_verify
```

The MCP process forwards calls to `POST /internal/embymedia/tool` on the DSH loopback listener. Caddy rejects `/internal/*`, and the Host rejects non-loopback peers. The stable `hermes-weixin` session keeps resource candidate IDs and operation-plan ownership valid across Weixin turns.

Example Weixin requests:

```text
现在有多少部电影？必须调用 embymedia MCP 精确统计。
检查电视剧追更库还有哪些缺集。
搜索这部剧的 115 资源，检查叶文件证据并创建补集计划。
执行刚才的计划并验证最终结果。
列出最近失败或部分完成的任务。
```

Every mutation follows `plan -> execute -> verify`. The private self-use deployment auto-allows the approval request, but write mode, canonical plan hashes, target revalidation, audit, and verification remain enforced. Destructive operations still require an explicit user request naming the intended outcome.

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

The EmbyMedia domain, database, client, planning, execution, and verification modules remain ordinary TypeScript classes. DSH-specific code is limited to the Host/tool adapters. Hermes uses the Host's loopback application route rather than a second SQL implementation, so both clients share one application path.

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

Hermes runs its messaging gateway as a user systemd service and discovers the local MCP server from `~/.hermes/config.yaml`. Only one Hermes gateway may poll a given Weixin iLink bot account.

## Safety

- Never commit `.env`, API keys, cookies, passwords, Weixin tokens, 115 access codes, or generated credential stores.
- Keep the DSH tool route on loopback and block `/internal/*` at every reverse proxy.
- Keep the scheduler disabled unless its write-policy path is explicitly reviewed and enabled.
- Treat `previewed`, `queued`, `running`, and `verifying` as non-terminal states; only verified `done` is success.
- Preserve local encrypted backups and complete an isolated restore before destructive infrastructure changes.

## Upstream and license

DeepSeek Harness remains the upstream framework. Preserve its license and third-party notices when rebasing or redistributing this private derivative.

[MIT](LICENSE) — see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
