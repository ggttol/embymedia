# EmbyMedia V2

English | [中文](README.zh.md)

EmbyMedia V2 is a self-hosted Go and Vue operations system for 115, CloudDrive2, Emby, STRM files, persistent tasks, REST, OpenAPI, and MCP. The supported runtime is one Go binary with an embedded browser application; Node, Cordis, DSH, and PostgreSQL are not production dependencies.

## Runtime

The Debian deployment runs the standalone binary as `embymedia-v2.service` on loopback port 3080. The same listener serves the Web UI, `/api/v1/*`, `/api/v1/openapi.json`, and Streamable HTTP MCP at `/mcp`. Optional legacy SSE listens on loopback port 3081, and `-mcp` serves the same registry over stdio.

Caddy authenticates browser traffic. Requests to `/api/v1/*` or `/mcp` carrying `X-Agent-Token` or `Authorization` bypass browser login, discard supplied browser identity headers, and are validated by the Go service. Stored token secrets are SHA-256 digests; plaintext is returned only once when the administrator creates a token.

SQLite at `/srv/embymedia/data/embymedia.db` owns settings, managed accounts, schedules, task attempts, Agent tokens, and audits. CloudDrive2, Emby, and the login service remain external dependencies managed by the Compose stack.

## Capabilities

- Manage multiple 115 accounts without returning cookies; refresh credential, VIP-expiry, and storage-quota state; select a default account and fall back to another active account.
- List, create, rename, move, and recycle 115 files and directories; inspect and save 115 shares; create share links; submit offline downloads; search the configured resource index.
- Read CloudDrive2 system and mount state through its version-matched gRPC API, measure filesystem capacity with `statfs`, and unmount/mount configured mount points with an authorized API token.
- Authenticate CloudDrive2 webhook events and debounce file changes into one persistent Emby refresh task.
- List Emby libraries, inspect one exact item ID, submit a specific-library refresh, track a full-library scan through Emby's scheduled-task state until its recorded completion, apply an explicit TMDB identity and images, and list missing-poster items.
- Synchronize STRM files from a configured media tree without following output symlinks, and verify that every STRM target stays inside the media root and exists.
- Execute validated background operations with schedule-linked execution IDs and durable attempts, start/end times, progress, results, errors, logs, cancellation, and explicit reviewed retry. Interrupted effectful work fails instead of replaying automatically.
- Expose the complete REST API and thirty-four explicit operational tools through stdio, Streamable HTTP, and legacy SSE MCP. Discovery tools resolve account, stored-file, share, item, session, task, and schedule IDs before writes.

## Browser workflows

Resource search preserves filters, loaded pages, and scroll position when returning from a detail page. Search, details, and favorites share the transfer destination; an unavailable saved directory blocks transfer until another destination is selected. Favorites support removal undo. The 115 file browser saves a pasted share link into the displayed account and directory, retains the link and extraction code after an error, and refreshes the directory after success. File dialogs identify the account and destination, retain failed input, and support keyboard focus containment. An immediate schedule run creates and displays its durable execution before the worker starts; active details refresh each second and show start, end, duration, provider progress, logs, errors, and the complete stored result. Long task histories use document scrolling and keep the final schedule above the mobile navigation. Clipboard failure opens a manual-copy dialog instead of reporting success.

## Agent access

The browser control center is `/agent`. It performs MCP initialization, tool discovery, safe read calls, and an OpenAPI request. While visible, the page refreshes connection discovery every 30 seconds and reads the latest 100 audit records every 10 seconds. Each tool shows its latest audited success or failure with the caller and timestamp. Autonomous tokens default to read/write access and 120 requests per minute. A 115 deletion requires a 15-minute, target-bound request that an authenticated browser user approves once; Agent tokens cannot approve it, enable the browser deletion switch, or call the REST deletion route. Emby library deletion is not exposed.

The MCP log query on `/agent` filters persisted calls by Agent token name, exact tool name, result, time range, and literal text in redacted parameters or output. Pages contain 25 records; totals, failure/denial rates, and per-tool average durations cover the entire matching set. Details contain stored, potentially truncated summaries, not original user instructions, Agent reasoning, or complete conversations. Use a separately named token for each Agent installation; shared tokens are indistinguishable, and tokenless stdio identity remains unknown.

Hermes uses the loopback Streamable HTTP endpoint and a full-access Agent token:

```sh
hermes mcp add embymedia --url http://127.0.0.1:3080/mcp --auth header
hermes mcp test embymedia
```

When prompted, enter the one-time secret created at `/agent` as the API key / Bearer token. Hermes stores the secret outside `config.yaml` and sends `Authorization: Bearer`; the server accepts that header and `X-Agent-Token`. The release installs `deploy/hermes/skills/embymedia-v2-operator/SKILL.md`; this skill names only the standalone registry and rejects the removed DSH tool vocabulary.

Stdio clients can launch a separately configured instance as the sole owner of its database. Do not point stdio at the running production database: startup refuses a second owner before migrations, task recovery, or scheduling. Use the production `/mcp` HTTP endpoint to share its accounts, tasks, and logs. An isolated stdio configuration is:

```json
{
  "mcpServers": {
    "embymedia": {
      "command": "/opt/embymedia-v2/current/bin/embymedia",
      "args": ["-mcp", "-db", "/srv/embymedia/stdio/embymedia.db"]
    }
  }
}
```

OpenClaw uses the same Streamable HTTP MCP registry. Replace `<one-time-token>` with a secret created at `/agent`, keep the resulting user configuration private, and probe the live tool list:

```sh
openclaw mcp add embymedia --url http://gaotao.cc:3080/mcp --transport streamable-http --header 'X-Agent-Token: <one-time-token>'
openclaw mcp probe embymedia
```

Oh My Pi reads the server from `.omp/mcp.json`; the header value resolves from the `EMBYMEDIA_AGENT_TOKEN` environment variable:

```json
{
  "mcpServers": {
    "embymedia": {
      "type": "http",
      "url": "http://gaotao.cc:3080/mcp",
      "headers": { "X-Agent-Token": "EMBYMEDIA_AGENT_TOKEN" }
    }
  }
}
```

The OpenAPI document remains available at `http://gaotao.cc:3080/api/v1/openapi.json` for clients with an OpenAPI importer. Public token-bearing Agent paths are forwarded directly to the fail-closed Go authorization middleware; headerless browser requests still use the login service.

## Repository layout

```text
cmd/server/                 binary assembly and embedded Vue assets
internal/api/               REST, OpenAPI, browser/Agent authorization
internal/mcp/               34-tool autonomous MCP registry
internal/service/           115, CloudDrive2, Emby, STRM, task, schedule, webhook logic
internal/storage/           monotonic SQLite schema and queries
web/                        Vue 3 browser application
deploy/                     Compose dependencies, Caddy, systemd, backups, Hermes skill
```

Legacy Harness source remains in the repository for historical development work but is absent from the supported V2 service graph, release installer, Caddy route, Hermes configuration, and backup/restore path.

<a id="run"></a><a id="run-from-source"></a>

## Development

Prerequisites: Go 1.26, Node.js 22.19 or newer, and pnpm 11.7.0. Node is needed only to build the embedded Vue assets.

```sh
pnpm install --frozen-lockfile --filter embymedia-web...
pnpm --dir web build
rm -rf cmd/server/dist && cp -R web/dist cmd/server/dist
go test ./internal/... ./cmd/server
go build -trimpath -o bin/embymedia ./cmd/server
```

The application defaults to loopback ports 8080 and 8081 for development. Use `-host`, `-port`, `-mcp-host`, `-mcp-port`, and `-db` for explicit runtime addresses and state. `-check-db` performs only SQLite opening and migrations; it starts no workers or schedules.

## Deployment

The host release root is `/opt/embymedia-v2/current`. Build `bin/embymedia-linux-amd64`, write its standard `sha256sum` file beside it as `bin/embymedia-linux-amd64.sha256`, then run:

```sh
sudo deploy/scripts/install-release.sh "$PWD" "$(date -u +%Y%m%dT%H%M%SZ)"
```

The installer verifies the artifact, installs an immutable release, checks the database as the service user, persists the webhook secret, and atomically switches `current`. Failed activation restores the previous release, installed configurations, and service states; database migrations and identity data are not reversed. A failed recovery retains its saved configurations and never deletes the active release.

## Safety

- Keep 115 cookies, Emby keys, CloudDrive tokens, webhook secrets, and Agent token plaintext out of the repository and logs.
- Browser file deletion requires `dangerous_actions_enabled` and an explicit UI confirmation. Agent tokens cannot use that route or enable its switch; MCP deletion requires a fresh target-bound request, one browser approval within 15 minutes, and one non-replayable execution.
- Tool discovery proves registration, not provider success. Report an operation as successful only from its non-error result, and report an asynchronous operation only after `status=completed`.
- A service restart marks interrupted effectful tasks failed. Inspect provider state before creating an explicit retry.
- CloudDrive container restarts can invalidate Emby's bind-mount view. Restart Emby after a CloudDrive container restart and verify `/media/.embymedia-health-canary` before serving playback.
- Backups preserve the original service states, quiesce V2 and dependency writers, and include the live browser users in `data/auth/http-login.json`. Isolated restoration validates that identity data and checks SQLite with `-check-db` before any production recovery.

## License

[MIT](LICENSE) — see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
