# EmbyMedia Operations

[English](README.md) | 中文

Emby、CloudDrive2、115、Hermes 与 DeepSeek Harness 的私有自托管媒体运营系统。

本仓库在 [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) 上扩展 EmbyMedia 业务包、迁移工具、加固后的 Debian 部署文件、浏览器运营台，以及供微信 Hermes 运营使用的完整 MCP 适配器。

<a id="run"></a>

## 当前部署

受支持部署运行于 Debian 13。Emby、CloudDrive2、PostgreSQL、DSH Web 应用、HTTP 登录服务、Caddy 与 Hermes 消息 gateway 运行在同一台主机。公网转发在 Caddy 终止；DSH、Emby、CloudDrive2、PostgreSQL 与 EmbyMedia MCP 应用路由只监听 loopback 或容器私有地址。

仓库不包含生产凭据。运行时 secret 位于 `/etc/embymedia/secrets/`，服务环境文件位于 `/etc/embymedia/`。

## 能力

- 盘点 Emby 媒体库、条目、STRM 文件、用户、任务、计划与审计记录。
- 按 Emby 类型返回精确条目数，包括 `Movie`、`Series` 与 `Episode`。
- 检查追更 Series 状态与已播缺集。
- 搜索 115 资源，使受保护分享凭据不进入模型可见结果，并检查递归叶文件证据。
- 为扫描、资源入库、Series 补集、元数据变更、用户策略变更、清理、删除与撤销创建 canonical 计划。
- 在 write mode、目标重验证、审计记录、partial 状态处理与独立验证保护下执行计划。
- 从 DSH 浏览器或微信 Hermes 操作同一个业务分派器，并获得相同结构化结果。

## Hermes 与 MCP

Hermes 通过 stdio 连接本机 `embymedia` MCP server。适配器暴露与 DSH 相同的十三个 wire tool：

```text
embymedia_health      embymedia_library     embymedia_resource
embymedia_series      embymedia_analyze     embymedia_task
embymedia_audit       embymedia_user        embymedia_schedule
embymedia_config      embymedia_plan        embymedia_execute
embymedia_verify
```

MCP 进程把调用转发到 DSH loopback listener 上的 `POST /internal/embymedia/tool`。Caddy 拒绝 `/internal/*`，Host 拒绝非 loopback 对端。稳定的 `hermes-weixin` session 使资源 candidate ID 与操作计划归属跨微信 turn 保持有效。

微信请求示例：

```text
现在有多少部电影？必须调用 embymedia MCP 精确统计。
检查电视剧追更库还有哪些缺集。
搜索这部剧的 115 资源，检查叶文件证据并创建补集计划。
执行刚才的计划并验证最终结果。
列出最近失败或部分完成的任务。
```

每次 mutation 均执行 `plan -> execute -> verify`。私有自用部署自动允许审批请求，但 write mode、canonical 计划哈希、目标重验证、审计与验证保持有效。Destructive 操作仍需用户明确说明预期结果。

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

EmbyMedia 领域、数据库、客户端、规划、执行与验证模块保持为普通 TypeScript 类。DSH 专属代码仅位于 Host/tool 适配器。Hermes 使用 Host 的 loopback 应用路由而不是第二套 SQL 实现，因此两个客户端共享同一个应用路径。

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

Hermes 把消息 gateway 作为 user systemd 服务运行，并从 `~/.hermes/config.yaml` 发现本机 MCP server。同一个微信 iLink bot 账号只能由一个 Hermes gateway 轮询。

## 安全

- 禁止提交 `.env`、API key、cookie、密码、微信 token、115 提取码或生成的凭据存储。
- DSH 工具路由必须保持 loopback，在每个 reverse proxy 拒绝 `/internal/*`。
- Scheduler 应保持禁用，除非其写策略路径已被明确审查并启用。
- `previewed`、`queued`、`running` 与 `verifying` 均不是终态；只有通过验证的 `done` 才是成功。
- 保留本地加密备份，并在 destructive 基础设施变更前完成隔离恢复。

## 上游与许可证

DeepSeek Harness 仍是上游框架。Rebase 或重新分发本私有衍生项目时，保留其许可证与第三方声明。

[MIT](LICENSE) — 参见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
