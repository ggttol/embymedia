# Agent Note: standalone V2 owns EmbyMedia operations

Status: implemented

[English](2026-09-05-embymedia-v2-standalone-operations.md) | 中文

## Problem

独立服务注册了十八个 MCP 工具，但注册状态掩盖了不完整行为：CloudDrive 状态使用虚构容量，重新挂载与创建 115 分享不可用，条目检查按标题搜索，任务 handler 模拟工作，OpenAPI 遗漏路由，Hermes 还加载了另一套 DSH 工具 registry 的技能。已部署 DSH 服务也在 Go 服务旁持续重启失败。

## Decision

Go 二进制是唯一部署的 EmbyMedia 应用 runtime。它提供 Vue、REST、OpenAPI 3.1、Streamable HTTP MCP、旧式 loopback SSE 与 stdio MCP。Debian Hermes 直接连接其 loopback `/mcp` endpoint，并只加载一个 V2 技能；该技能的操作说明仅使用已注册的十八个工具。

Provider 工具执行真实 provider 操作。115 client 使用选定账号凭据执行列表、创建、重命名、移动、删除、转存分享、创建分享链接与提交离线下载。CloudDrive2 client 使用版本匹配的 gRPC 方法读取系统状态与挂载清单，并执行卸载和挂载；文件系统容量来自 `statfs`。Emby 条目检查定位一个确切条目 ID，元数据应用会检查上游响应。

持久任务队列只接受其已公布类型：Emby 刷新与匹配、115 分享与离线操作，以及 STRM 同步或验证。每次尝试都持久记录状态、进度、结果、错误与日志。取消通过 request context 传播。服务重启时，执行中的 effectful 工作会变为失败并要求显式重试，因为自动重放可能重复已经被上游接受的写入。

REST 与 MCP 共享 token 身份验证、读写 scope、逐 token 限流桶、最近使用时间更新与 secret-redacted 审计记录。文件删除路由还要求持久化的破坏性操作开关。OpenAPI 通过唯一 operation ID 与请求 schema 列出每个已注册 API 操作。CloudDrive webhook 使用独立 secret 验证投递，并把文件变动防抖为一个真实 Emby 刷新任务。

Loopback 登录服务负责浏览器用户、密码 hash、签名 session 与仅管理员可用的用户 API。管理员与操作员都能使用媒体运维功能；只有管理员能创建、停用、重置或删除浏览器用户。密码变更会轮换该用户的 session version，且服务始终保留至少一个启用的管理员。Caddy 只暴露登录 API，并把已验证的用户名与角色转发给应用。

## Alternatives considered

**保留 DSH 作为生产 dispatcher。** 不采用，因为 V2 部署会保留 Node、Cordis、第二套工具词汇，以及独立二进制旁一个持续失败的服务。

**保留只返回 unavailable 错误的已注册工具。** 不采用，因为 discovery 会继续夸大可执行能力；provider 前提仍可能失败，但每个工具都有真实实现路径。

**服务重启后自动重放运行中任务。** 不采用，因为本地进程丢失终态更新前，115 与 Emby 可能已经接受操作。显式重试保留未知副作用警告。

## Consequences

生产 runtime 不再需要 DSH 服务、Node MCP bridge、Cordis 部署 patch、PostgreSQL 服务或 Node control helper。CloudDrive 重新挂载要求具有 mount 权限的 API token；STRM 任务要求明确配置媒体、输出与 Emby 路径。工具 discovery 与执行证据仍是不同事实，因此 Agent 页面执行安全只读调用，并分别标记仅发现的工具。任务中心以带类型字段和可读计划的用户工作流呈现 provider 操作；原始任务 tag 与 JSON 仅保留在 API 和持久化数据中。
