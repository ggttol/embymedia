# EmbyMedia 架构

[English](architecture.md) | 中文

## 概要

EmbyMedia 在一个 Go 进程中执行媒体工作流，并向浏览器与 Agent 客户端提供同一组服务操作。SQLite 持久保存配置与任务；Emby、CloudDrive2、115 和资源索引各自拥有外部状态。

## 目录

- [运行时与所有权](#运行时与所有权)
- [持久任务](#持久任务)
- [安全与媒体身份](#安全与媒体身份)

## 运行时与所有权

[`cmd/server`](../cmd/server) 组装 REST、内嵌 Vue 资源、OpenAPI、Streamable HTTP MCP、旧式 SSE 及可选 stdio。[`internal/api`](../internal/api) 与 [`internal/mcp`](../internal/mcp) 调用共享的 [`internal/service`](../internal/service) 实现；两种传输均不定义第二个媒体执行器。[`web`](../web) 是独立 pnpm 项目，其构建结果成为内嵌资源。

[`internal/storage`](../internal/storage) 负责单调递增的 SQLite 迁移、设置、账号、令牌摘要、任务尝试、自动计划及审计。进程在迁移与队列恢复前锁定数据库。第二个 HTTP、stdio 或维护进程不能共享所有权。通过 HTTP MCP 共享运行中的实例。

审计写入将时间统一为 UTC 并保留纳秒精度。时间筛选比较实际时刻且包含起止边界，不依赖服务器的本地时区。

部署将应用置于 Caddy 后方的回环地址。Python HTTP 登录服务负责浏览器用户及签名会话；Compose 负责 Emby 与 CloudDrive2。V2 不运行 Node 应用服务器、Cordis 插件加载器、SDK runtime 或 PostgreSQL 实例。[运维指南](operations.zh.md)负责安装路径与恢复流程。

## 持久任务

队列持久保存等待执行的工作与尝试结果；自动计划创建关联执行。中断的有副作用任务通常会失败并等待明确检查，不会重放 provider 修改。夸克到 115 导入是受限例外：持久保存的源身份、中转偏移、分片及已验证目标身份让启动恢复可以协调后续执行，无须再次转存分享或重复写入字节；provider 状态有歧义时仍会失败并等待检查。

Emby 全库刷新先同步并验证 STRM 输出，再请求扫描并等待 provider 完成。STRM 协调保留原始媒体路径，拒绝有歧义的规范目标，并要求挂载 canary 可读后才能移除过期生成输出。生成目录通过文件系统 ACL 保持 Emby 共享访问权限。

自动补集分别搜索 115 与夸克，并通过实时分享树判断候选是否可用，不按资源索引的健康标记筛选。预检模式不执行 provider 写操作，并报告包含 provider 的证据；同一集有多个可用资源时保持明确冲突，直到操作者选择资源。普通单集要求实时路径使用规范标题或原始 Series 标题；候选与分享元数据只能佐证规范年份或 TMDB ID，因此嘈杂的元数据标题不能覆盖实时路径。选中的 115 叶子继续直接进入准确目录；选中的夸克叶子由持久子任务处理：服务器在转存分享前验证并持久保存已配置的 115 媒体库、规范 Emby Series、准确目标目录、源 ID 与预期集号，随后恢复下载和上传检查点，并且仅在 115、STRM 与 Emby 均验证后完成。公开任务请求不能提供内部目标绑定。[provider 限定补集决策](../.agents/notes/implemented/feature/2026-09-13-provider-qualified-episode-completion.zh.md)定义这些边界。

## 安全与媒体身份

REST 与 MCP 共享 Agent 身份验证、scope、限流及脱敏审计。Caddy 在公开 Agent 路由上移除调用方提供的浏览器身份；明确无效的凭据不能退回浏览器信任。浏览器拥有的删除开关与目标绑定审批独立于 Agent 读写 scope。

缺集完成要求预期的 TMDB 绑定 Emby Series 拥有请求的剧集，不能仅凭 115 文件或 STRM 路径可见判断。Emby 原生删除要求经过授权的 Emby 管理员设备会话，并重新验证原始源身份。部分完成结果保持可见，不会静默重放。[Series 决策](../.agents/notes/implemented/bug-fix/2026-08-31-canonical-series-missing-episode-binding.zh.md)记录其理由。
