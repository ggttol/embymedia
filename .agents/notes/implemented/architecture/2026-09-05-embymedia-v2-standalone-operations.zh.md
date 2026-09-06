# Agent Note: standalone V2 owns EmbyMedia operations

Status: implemented

[English](2026-09-05-embymedia-v2-standalone-operations.md) | 中文

## Problem

独立服务注册了十八个 MCP 工具，但注册状态掩盖了不完整行为：CloudDrive 状态使用虚构容量，重新挂载与创建 115 分享不可用，条目检查按标题搜索，任务 handler 模拟工作，OpenAPI 遗漏路由，Hermes 还加载了另一套 DSH 工具 registry 的技能。已部署 DSH 服务也在 Go 服务旁持续重启失败。

## Decision

Go 二进制是唯一部署的 EmbyMedia 应用 runtime。它提供 Vue、REST、OpenAPI 3.1、Streamable HTTP MCP、旧式 loopback SSE 与 stdio MCP。Debian Hermes 直接连接其 loopback `/mcp` endpoint，并为已注册的三十四个工具加载一个 V2 技能。

Provider 工具执行真实 provider 操作。115 客户端使用所选账号凭据列出、创建、重命名、移动、删除与转存文件，创建分享链接，并提交离线下载。CloudDrive2 客户端通过版本匹配的 gRPC 方法读取系统状态与挂载清单、卸载和挂载；文件系统容量来自 `statfs`。CloudDrive2 返回容器内路径，已配置挂载路径标识健康检查所用的对应宿主机文件系统路径。Emby 条目检查按一个确切条目 ID 寻址，元数据应用会检查上游响应，并且每个 Emby 请求都通过 `X-Emby-Token` 发送 API key，而不是使用可能出现在持久错误中的 URL。

持久任务队列只接受其已公布类型：Emby 刷新与匹配、115 分享与离线操作，以及 STRM 同步或验证。每次自动计划启动都会原子创建关联执行，并更新计划的最近执行 ID 与启动时间。每次尝试都持久记录开始和完成时间、进度、结果、错误与日志。Emby 全库刷新会启动 `RefreshLibrary` scheduled task，跟随 provider 报告的进度，并且只有在 Emby 记录终态结果后才完成；指定媒体库的条目刷新只记录请求已接受，因为该 endpoint 不暴露完成状态。缺失海报检查会返回完整总数，以及带路径和 provider ID 的有界页面。STRM 同步只会在媒体挂载 canary 证明源挂载存在时删除目标缺失的生成记录，从而协调输出；它会保留有效的非生成记录，并报告跳过清理。检查执行完成但包含缺失或无效条目时，仍是执行成功，但会保留明确发现。取消操作会停止该执行启动的全库扫描。服务重启时，执行中的 effectful 工作会变为失败并要求显式重试，因为自动重放可能重复已经被上游接受的写入。Scheduler 从已解析 cron schedule 直接计算并持久化首次下次运行时间，而不是在 cron loop 启动前读取值为零的 `cron.Entry.Next`。

REST 与 MCP 共享 token 身份验证、读写 scope、逐 token 限流桶、最近使用时间更新与 secret-redacted 审计记录。自主运行令牌默认具有读写权限和每分钟 120 次请求额度。非破坏性工具可以先发现账号、分享、离线任务、Emby 条目和会话、持久任务与自动计划，再执行修改。删除 115 内容使用两个工具：第一个把刚读取的名称和 ID 绑定到有效期 15 分钟的待确认请求；已登录浏览器用户批准该准确请求；第二个工具只能消费一次。Agent token 不能批准请求、开启浏览器删除开关或调用 REST 文件删除路由。系统不暴露 Emby 媒体库删除能力。OpenAPI 通过唯一 operation ID 与请求 schema 列出每个已注册 API 操作。CloudDrive 重新挂载会自行检查 Emby 实时会话，并在播放活跃时拒绝执行。

Loopback 登录服务负责浏览器用户、密码 hash、签名 session 与仅管理员可用的用户 API。管理员与操作员都能使用媒体运维功能；只有管理员能创建、停用、重置或删除浏览器用户。密码变更会轮换该用户的 session version，且服务始终保留至少一个启用的管理员。Caddy 只暴露登录 API，并把已验证的用户名与角色转发给应用。

浏览器状态区分未检测、检测中、成功、失败与过期观测；MCP 工具发现不等于执行成功。Agent 页面可见时，每 30 秒刷新连接与工具发现，每 10 秒读取最近 100 条审计记录，并把每个工具最近一次持久化结果关联到调用方和时间。页面隐藏时暂停轮询，轻量连接刷新不会重复执行 provider 只读调用。浏览器使用页面滚动，而不是固定高度的嵌套 canvas；手机 safe-area 留白会让最后的控件保持在固定导航上方。转存页面共享明确的目标目录，已保存 CID 不可用时拒绝转存，而非悄悄选择根目录。复制成功必须以剪贴板写入完成为依据；非安全 HTTP 与剪贴板权限被拒绝时提供手动选择。这些区别避免界面声称未经观测的读取或写入已经完成。

审计查询在同一个 SQLite 读取事务中先统计全部匹配记录，再进行分页。Agent 身份来自已验证令牌的名称，而非猜测的客户端品牌；工具输入与有长度限制的输出不能重建用户指令或模型思考过程。HTTP、stdio 和维护进程必须独占数据库，因为各自恢复任务和启动调度器可能重复已接受的操作。Cron 回调在与删除和替换操作共用的锁内验证注册身份，因此过期回调不能恢复计划。

公开 Agent 代理路由会删除调用方提供的浏览器身份 header。Fetch 元数据不构成身份验证，显式无效凭据不能退回浏览器信任。登录签发在密码计算后重新检查密码与 session version；用户更新只有在持久化成功后才发布。部署恢复保留当前版本及已安装配置，备份包含实际浏览器用户数据库。Provider 文件发现保留准确文件 ID；CloudDrive 主机路径映射要求完整路径对应，而非只比较末级名称。

## Alternatives considered

**保留 DSH 作为生产 dispatcher。** 不采用，因为 V2 部署会保留 Node、Cordis、第二套工具词汇，以及独立二进制旁一个持续失败的服务。

**保留只返回 unavailable 错误的已注册工具。** 不采用，因为 discovery 会继续夸大可执行能力；provider 前提仍可能失败，但每个工具都有真实实现路径。

**服务重启后自动重放运行中任务。** 不采用，因为本地进程丢失终态更新前，115 与 Emby 可能已经接受操作。显式重试保留未知副作用警告。

## Consequences

生产 runtime 不再需要 DSH 服务、Node MCP bridge、Cordis 部署 patch、PostgreSQL 服务或 Node control helper。STRM 任务要求明确配置媒体、输出与 Emby 路径。Discovery 与执行证据仍是不同事实，Agent 页面分别显示两者。大多数操作只使用一个 full-access token，不要求逐项批准；只有媒体删除必须经过目标绑定的浏览器确认。任务和自动计划工具提供带类型的发现与修改，Agent 不需要猜测 ID 或依赖浏览器操作。
