# EmbyMedia Operations

[English](README.md) | 中文

Emby、CloudDrive2、115、Hermes 与 DeepSeek Harness 的私有自托管媒体运营系统。

本仓库在 [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) 上扩展 EmbyMedia 业务包、迁移工具、加固后的 Debian 部署文件、浏览器运营台，以及供微信 Hermes 运营使用的完整 MCP 适配器。

<a id="run"></a>

## 当前部署

受支持部署运行于 Debian 13。Emby、CloudDrive2、PostgreSQL、DSH Web 应用、独立 Go/Vue 运营服务、HTTP 登录服务、Caddy 与 Hermes 消息 gateway 运行在同一台主机。公网转发与登录在 Caddy 终止；各服务 listener 属于主机本地或容器私有部署细节，不作为通用公网入口。

仓库不包含生产凭据。运行时 secret 位于 `/etc/embymedia/secrets/`，服务环境文件位于 `/etc/embymedia/`。

## 能力

- 盘点 Emby 媒体库、条目、STRM 文件、用户、任务、计划与审计记录。
- 按 Emby 类型返回精确条目数，包括 `Movie`、`Series` 与 `Episode`。
- 检查追更 Series 状态与已播缺集。
- 搜索 115 资源，使受保护分享凭据不进入模型可见结果，并检查递归叶文件证据。
- 为扫描、资源入库、Series 补集、元数据变更、用户策略变更、清理、删除与撤销创建 canonical 计划。
- 在 write mode、目标重验证、审计记录、partial 状态处理与独立验证保护下执行计划。
- 从 DSH 操作规划与验证工作流，或通过微信 Hermes 使用独立 Go MCP registry 中已注册的 115、CloudDrive2、Emby、任务与系统操作。

## Hermes 与 MCP

Hermes v0.21 通过 Streamable HTTP 连接独立 Go 服务的 `http://127.0.0.1:3080/mcp`。其 `embymedia` registry 可发现十八个工具。发现成功只证明协议注册；每次调用仍可能因为上游配置、权限或明确不可用的 provider 操作而失败。

```sh
hermes mcp add embymedia --url http://127.0.0.1:3080/mcp
hermes mcp test embymedia
```

Go 服务在 3080 端口提供 MCP、Web UI 与 REST。`/agents` 是浏览器控制台，不是 MCP transport 地址。Caddy 验证公网 3080 访问；同主机 Hermes 使用 loopback，不依赖未发布的 3081 旧式 SSE listener。

微信请求示例：

```text
列出 Emby 媒体库和路径。
搜索名称中包含这部剧的 115 资源。
刷新 Emby 媒体库。
检查 CloudDrive2 挂载清单。
查询这个后台任务的状态和错误。
```

MCP mutation 工具直接执行已配置的 provider 操作并返回上游错误。分享链接生成、CloudDrive2 重新挂载与运行中任务取消会返回明确的不可用错误，而非合成成功结果。操作需要 canonical `plan -> execute -> verify` 控制时，应使用 DSH 规划路径。

## 仓库布局

```text
packages/embymedia/operations/
packages/embymedia/preset/
packages/embymedia/ui/
apps/embymedia-migrate/
apps/embymedia-control-helper/
deploy/
migration/
```

本仓库包含两条应用路径。DSH packages 负责 canonical 规划、执行与验证工作流。独立 Go 服务负责其 Vue 控制台、REST API、SQLite 状态与十八工具 MCP registry；已部署 Hermes 配置使用该 Go registry，而不是 DSH dispatcher。

<a id="run-from-source"></a>

## 开发

前置条件：Node.js 22.19 或更新版本、pnpm，以及供数据库测试使用的 PostgreSQL。

```sh
pnpm install
pnpm exec tsc -b packages/embymedia/operations/tsconfig.json
pnpm exec vitest run --root . packages/embymedia/operations/tests --no-file-parallelism
pnpm --filter @embymedia/dsh-operations bundle
```

MCP 部署包自包含：

```sh
cd deploy/hermes/embymedia-mcp
npm ci --ignore-scripts
node --check src/index.js
```

## 部署

部署文件假设 `/opt/embymedia/current` 指向活动 release，`/srv/embymedia/data` 持有持久数据。应用到其他主机前应审查 `deploy/` 下的每个文件；网络名称、路径、UID 与存储布局均为部署专属值。

更新 operations Host 后执行：

```sh
pnpm exec tsc -b packages/embymedia/operations/tsconfig.json
pnpm --filter @embymedia/dsh-operations bundle
sudo systemctl restart embymedia-dsh.service
```

Hermes 把消息 gateway 作为 user systemd 服务运行，并从 `~/.hermes/config.yaml` 发现 `http://127.0.0.1:3080/mcp`。同一个微信 iLink bot 账号只能由一个 Hermes gateway 轮询。

## 安全

- 禁止提交 `.env`、API key、cookie、密码、微信 token、115 提取码或生成的凭据存储。
- 同主机客户端应使用 loopback MCP，公网 3080 必须经过 Caddy 身份验证，并在每个 reverse proxy 拒绝 DSH `/internal/*` 路由。
- Scheduler 应保持禁用，除非其写策略路径已被明确审查并启用。
- `previewed`、`queued`、`running` 与 `verifying` 均不是终态；只有通过验证的 `done` 才是成功。
- 保留本地加密备份，并在 destructive 基础设施变更前完成隔离恢复。

## 上游与许可证

DeepSeek Harness 仍是上游框架。Rebase 或重新分发本私有衍生项目时，保留其许可证与第三方声明。

[MIT](LICENSE) — 参见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
