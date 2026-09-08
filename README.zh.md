# EmbyMedia V2

[English](README.md) | 中文

EmbyMedia V2 是面向 115、CloudDrive2、Emby、STRM 文件、持久任务、REST、OpenAPI 与 MCP 的自托管 Go 和 Vue 运营系统。受支持 runtime 是一个内嵌浏览器应用的 Go 二进制；Node、Cordis、DSH 与 PostgreSQL 都不是生产依赖。

## Runtime

Debian 部署通过 `embymedia-v2.service` 在 loopback 3080 端口运行独立二进制。同一个 listener 提供 Web UI、`/api/v1/*`、`/api/v1/openapi.json` 与 `/mcp` 上的 Streamable HTTP MCP。可选的旧式 SSE 监听 loopback 3081 端口，`-mcp` 则通过 stdio 提供相同 registry。

Caddy 验证浏览器流量。携带 `X-Agent-Token` 或 `Authorization` 的 `/api/v1/*` 或 `/mcp` 请求绕过浏览器登录，删除调用方提供的浏览器身份 header，再由 Go 服务校验。数据库只保存 token secret 的 SHA-256 摘要；管理员创建 token 时，明文只返回一次。

`/srv/embymedia/data/embymedia.db` 中的 SQLite 负责设置、受管账号、定时任务、任务执行记录、Agent token 与审计。CloudDrive2、Emby 与登录服务仍是由 Compose stack 管理的外部依赖。

## 能力

- 管理多个 115 账号且不返回 Cookie；刷新凭据、VIP 到期与存储配额状态；选择默认账号，并在默认账号不可用时选用另一个 active 账号。
- 列出、创建、重命名、移动与回收 115 文件和目录；检查并转存 115 分享；创建分享链接；提交离线下载；搜索已配置的资源索引。
- 通过版本匹配的 gRPC API 读取 CloudDrive2 系统和挂载状态，以 `statfs` 测量文件系统容量，并用授权 API token 卸载和挂载已配置挂载点。
- 验证 CloudDrive2 webhook 事件，并把文件变化防抖为一个持久入库任务；该任务先完成 STRM 同步，再启动 Emby 扫描。
- 列出 Emby 媒体库，按确切条目 ID 检查，提交指定媒体库刷新，在全库刷新前同步全部媒体，跟踪该 Emby 扫描直至记录完成，应用明确的 TMDB identity 与图片，并返回缺失海报的完整总数，以及最多 100 个路径和 provider ID。
- 从已配置媒体树同步 STRM 文件且不跟随输出 symlink；无论服务 umask 如何，都确保生成目录可由 Emby 读取和穿过；只有媒体挂载 canary 存在时，才删除目标已消失的生成 STRM 文件；并验证每个剩余 target 均位于媒体根目录内且真实存在。
- 使用关联自动计划的执行 ID，以及持久化的尝试记录、开始／结束时间、进度、结果、错误、日志、取消与显式评审重试来执行经过校验的后台操作。检查执行完成但发现媒体缺失或无效时，结果会保留为待处理发现，而非显示为健康。中断的 effectful 工作会失败，而非自动重放。
- 通过 stdio、Streamable HTTP 与旧式 SSE MCP 暴露完整 REST API 和三十四个明确的运营工具。Discovery 工具先解析账号、已有文件、分享、条目、会话、任务与计划 ID，再执行写操作。

## 浏览器工作流

从详情页返回时，资源检索保留筛选条件、已加载分页与滚动位置。检索、详情与收藏共享转存目标；已保存的目录不可用时，必须重新选择目标才能转存。收藏支持撤销移除。115 文件浏览器把粘贴的分享链接转存到当前显示的账号与目录；失败时保留链接和提取码，成功后刷新目录。文件弹窗明确账号与目标目录，失败时保留输入，并将键盘焦点限制在弹窗内。立即执行自动计划时，页面会在 worker 启动前创建并显示持久执行记录；执行中的详情每秒更新，并显示开始、结束、耗时、provider 进度、日志、错误与完整存储结果。已完成但包含 STRM 或缺失海报发现的检查会计入待处理数量并使用警告样式，正常操作仍显示绿色。较长的任务历史使用页面滚动，并确保最后一项自动计划位于手机导航上方。剪贴板复制失败会打开手动复制弹窗，而非显示成功。

## Agent 接入

浏览器控制中心位于 `/agent`。该页面执行 MCP 初始化、工具发现、安全只读调用与 OpenAPI 请求。页面可见时，每 30 秒刷新连接与工具发现，每 10 秒读取最近 100 条审计记录。每个工具显示最近一次审计调用的成功或失败、调用方与时间。自主运行令牌默认具有读写权限和每分钟 120 次请求额度。删除 115 内容需要一个有效期 15 分钟、绑定准确目标的请求，并由已登录浏览器用户一次性批准；Agent token 不能批准请求、开启浏览器删除开关或调用 REST 删除路由。系统不暴露 Emby 媒体库删除能力。

`/agent` 的 MCP 日志查询可按 Agent 令牌名称、准确工具名、结果、时间范围，以及脱敏参数或输出中的字面文本筛选持久化调用。每页显示 25 条；调用总数、失败率／拒绝率和逐工具平均耗时覆盖全部匹配记录。详情是已保存且可能截断的摘要，不包含用户原始指令、Agent 思考过程或完整对话。每个 Agent 实例使用单独命名的令牌；共用令牌无法区分，无令牌 stdio 的身份保留为未知。

Hermes 使用 loopback Streamable HTTP endpoint 与 full-access Agent token：

```sh
hermes mcp add embymedia --url http://127.0.0.1:3080/mcp --auth header
hermes mcp test embymedia
```

出现提示时，把 `/agent` 创建后只显示一次的 secret 作为 API key / Bearer token 输入。Hermes 会把 secret 存储在 `config.yaml` 之外，并发送 `Authorization: Bearer`；服务端同时接受该 header 与 `X-Agent-Token`。Release 会安装 `deploy/hermes/skills/embymedia-v2-operator/SKILL.md`；该技能只使用独立 registry，并拒绝已移除的 DSH 工具词汇。

Stdio 客户端可以启动单独配置的实例，并独占其数据库。不要让 stdio 指向正在运行的生产数据库：第二个进程会在迁移、任务恢复和调度之前被拒绝。需要共享生产账号、任务和日志时，使用生产 `/mcp` HTTP endpoint。隔离的 stdio 配置如下：

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
internal/mcp/               34-tool autonomous MCP registry
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

Installer 校验 artifact，安装不可变 release，以服务用户检查数据库，持久化 webhook secret，并原子切换 `current`。它安装 Caddy systemd override 以禁用环境变量日志，并 reload 运行中的 Caddy 进程而不中断共享路由。激活失败时恢复上一版本、已安装配置与服务状态；数据库迁移和身份数据不会回退。恢复失败会保留已保存配置，且不会删除当前版本。

## 安全

- 禁止把 115 Cookie、Emby key、CloudDrive token、webhook secret 或 Agent token 明文写入仓库和日志。
- 浏览器文件删除要求启用 `dangerous_actions_enabled` 并在 UI 中明确确认。Agent token 不能使用该路由或开启其开关；MCP 删除要求提交绑定最新目标的请求，由浏览器用户在 15 分钟内批准，并且只能执行一次。
- 工具 discovery 只证明注册，不证明 provider 成功。仅根据无错误 result 报告操作成功；异步操作只有在 `status=completed` 后才能报告成功。
- 服务重启会把中断的 effectful 任务标为失败。创建显式 retry 前应检查 provider 当前状态。
- `embymedia-clouddrive-recovery.timer` 每分钟检查容器和挂载健康标记。容器不健康或连续三次无法读取健康标记时，它会执行有界的 CloudDrive 和 Emby 重启、惰性删除失效的 FUSE 挂载、检查宿主机及 `/media` 健康标记，并恢复 V2；十五分钟冷却期防止重启循环。服务栈停止时也会删除残留的 FUSE 挂载。
- 备份期间 V2 和 Compose 服务栈保持运行。备份会对每个检测到的数据库使用 SQLite 在线备份 API，重试复制过程中发生变化的普通文件，在生成检查点一致的副本后省略 WAL／SHM companion，通过 `PRAGMA quick_check` 验证每个数据库，再把私有暂存树交给 Restic。隔离恢复会识别该树，恢复规范路径和 owner，验证浏览器 identity，并通过 `-check-db` 检查 SQLite，之后才能执行任何生产恢复。

## 许可证

[MIT](LICENSE) — 参见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
