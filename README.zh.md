# EmbyMedia V2

[English](README.md) | 中文

EmbyMedia V2 是面向 115、CloudDrive2、Emby、STRM 文件、持久任务、REST、OpenAPI 与 MCP 的自托管 Go 和 Vue 运营系统。受支持 runtime 是一个内嵌浏览器应用的 Go 二进制；Node、Cordis、DSH 与 PostgreSQL 都不是生产依赖。

## Runtime

Debian 部署通过 `embymedia-v2.service` 在 loopback 3080 端口运行独立二进制。同一个 listener 提供 Web UI、`/api/v1/*`、`/api/v1/openapi.json` 与 `/mcp` 上的 Streamable HTTP MCP。可选的旧式 SSE 监听 loopback 3081 端口，`-mcp` 则通过 stdio 提供相同 registry。

Caddy 验证浏览器流量。携带 `X-Agent-Token` 的 `/api/v1/*` 或 `/mcp` 请求绕过浏览器登录，并由 Go 服务校验。数据库只保存 token secret 的 SHA-256 摘要；管理员创建 token 时，明文只返回一次。

`/srv/embymedia/data/embymedia.db` 中的 SQLite 负责设置、受管账号、定时任务、任务执行记录、Agent token 与审计。CloudDrive2、Emby 与登录服务仍是由 Compose stack 管理的外部依赖。

## 能力

- 管理多个 115 账号且不返回 Cookie；刷新凭据、VIP 到期与存储配额状态；选择默认账号，并在默认账号不可用时选用另一个 active 账号。
- 列出、创建、重命名、移动与回收 115 文件和目录；检查并转存 115 分享；创建分享链接；提交离线下载；搜索已配置的资源索引。
- 通过版本匹配的 gRPC API 读取 CloudDrive2 系统和挂载状态，以 `statfs` 测量文件系统容量，并用授权 API token 卸载和挂载已配置挂载点。
- 验证 CloudDrive2 webhook 事件，并把文件变化防抖为一个持久 Emby 刷新任务。
- 列出 Emby 媒体库，按确切条目 ID 检查，刷新一个或全部媒体库，应用明确的 TMDB identity 与图片，并列出缺失海报的条目。
- 从已配置媒体树同步 STRM 文件且不跟随输出 symlink，并验证每个 STRM target 均位于媒体根目录内且真实存在。
- 以持久执行记录、进度、结果、错误、日志、取消与显式评审重试执行经过校验的后台操作。中断的 effectful 工作会失败，而非自动重放。
- 通过 OpenAPI 3.1 暴露完整 REST API，并通过 stdio、Streamable HTTP 与旧式 SSE MCP 暴露相同的十八个运营工具。

## Agent 接入

浏览器控制中心位于 `/agent`。该页面执行真实 MCP 初始化、工具发现、安全只读调用与 OpenAPI 请求。仅被发现的工具和已成功或失败执行只读调用的工具采用不同状态标记。

Hermes 使用 loopback Streamable HTTP endpoint 与 full-access Agent token：

```sh
hermes mcp add embymedia --url http://127.0.0.1:3080/mcp --auth header
hermes mcp test embymedia
```

出现提示时，把 `/agent` 创建后只显示一次的 secret 作为 API key / Bearer token 输入。Hermes 会把 secret 存储在 `config.yaml` 之外，并发送 `Authorization: Bearer`；服务端同时接受该 header 与 `X-Agent-Token`。Release 会安装 `deploy/hermes/skills/embymedia-v2-operator/SKILL.md`；该技能只使用独立 registry，并拒绝已移除的 DSH 工具词汇。

Claude Desktop 可以通过 stdio 启动二进制：

```json
{
  "mcpServers": {
    "embymedia": {
      "command": "/opt/embymedia-v2/current/bin/embymedia",
      "args": ["-mcp", "-db", "/srv/embymedia/data/embymedia.db"]
    }
  }
}
```

OpenClaw 使用同一个 Streamable HTTP MCP registry。把 `<one-time-token>` 替换为 `/agent` 创建的 secret，保护生成的用户配置，并探测实时工具列表：

```sh
openclaw mcp add embymedia --url http://gaotao.cc:3080/mcp --transport streamable-http --header 'X-Agent-Token: <one-time-token>'
openclaw mcp probe embymedia
```

Oh My Pi 从 `.omp/mcp.json` 读取服务器；header 值从 `EMBYMEDIA_AGENT_TOKEN` 环境变量解析：

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

具有 OpenAPI importer 的客户端仍可使用 `http://gaotao.cc:3080/api/v1/openapi.json`。公网携带 token 的 Agent 路径直接转发到 fail-closed Go 授权 middleware；不含该 header 的浏览器请求仍使用登录服务。

## 仓库布局

```text
cmd/server/                 binary assembly and embedded Vue assets
internal/api/               REST, OpenAPI, browser/Agent authorization
internal/mcp/               eighteen-tool MCP registry
internal/service/           115, CloudDrive2, Emby, STRM, task, schedule, webhook logic
internal/storage/           monotonic SQLite schema and queries
web/                        Vue 3 browser application
deploy/                     Compose dependencies, Caddy, systemd, backups, Hermes skill
```

仓库仍保留历史 Harness 源码用于历史开发工作，但它不在受支持 V2 的 service graph、release installer、Caddy route、Hermes 配置或备份／恢复路径中。

<a id="run"></a><a id="run-from-source"></a>

## 开发

前置条件：Go 1.26、Node.js 22.19 或更新版本，以及 pnpm 11.7.0。Node 只用于构建内嵌 Vue assets。

```sh
pnpm install --frozen-lockfile --filter embymedia-web...
pnpm --dir web build
rm -rf cmd/server/dist && cp -R web/dist cmd/server/dist
go test ./internal/... ./cmd/server
go build -trimpath -o bin/embymedia ./cmd/server
```

开发环境默认使用 loopback 8080 与 8081 端口。通过 `-host`、`-port`、`-mcp-host`、`-mcp-port` 和 `-db` 明确指定 runtime 地址与状态。`-check-db` 只执行 SQLite 打开与迁移，不启动 worker 或 schedule。

## 部署

主机 release 根目录是 `/opt/embymedia-v2/current`。构建 `bin/embymedia-linux-amd64`，并在同目录写入标准 `sha256sum` 文件 `bin/embymedia-linux-amd64.sha256`，然后运行：

```sh
sudo deploy/scripts/install-release.sh "$PWD" "$(date -u +%Y%m%dT%H%M%SZ)"
```

Installer 会校验 artifact，把部署 assets 复制到不可变 release，执行无副作用数据库检查，持久化共享 webhook secret，原子切换 `current`，安装 units 与 Hermes skill，启动依赖 stack 与登录服务，启用 backup timer，启动 V2，并在 loopback OpenAPI probe 失败时回滚 symlink。

## 安全

- 禁止把 115 Cookie、Emby key、CloudDrive token、webhook secret 或 Agent token 明文写入仓库和日志。
- 除非明确启用 `dangerous_actions_enabled`，文件删除会返回 403；Web UI 还要求确认。
- 工具 discovery 只证明注册，不证明 provider 成功。仅根据无错误 result 报告操作成功；异步操作只有在 `status=completed` 后才能报告成功。
- 服务重启会把中断的 effectful 任务标为失败。创建显式 retry 前应检查 provider 当前状态。
- CloudDrive 容器重启可能使 Emby 的 bind-mount view 失效。CloudDrive 容器重启后应重启 Emby，并在提供播放前验证 `/media/.embymedia-health-canary`。
- 备份保留原始服务状态，先 quiesce V2 与依赖 writer，再在隔离路径中通过 `-check-db` 恢复，之后才能执行任何生产恢复。

## 许可证

[MIT](LICENSE) — 参见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
