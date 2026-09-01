# Agent Note: EmbyMedia tools keep one application path across DSH and MCP

Status: implemented

[English](2026-09-02-embymedia-full-mcp-adapter.md) | 中文

## Problem

EmbyMedia 运营者同时使用 DSH 浏览器与 Hermes 消息 gateway。另一套直接查询 PostgreSQL 或 Emby 的实现无法保留资源候选、canonical 计划、目标重验证、执行审计与独立验证。两个客户端会报告不同事实，并采用不同的写入保护。

## Decision

EmbyMedia Host 拥有唯一的工具分派器。DSH `/tools` 在进程内调用它；受信的同主机适配器通过 loopback web listener 上的 `POST /internal/embymedia/tool` 调用同一分派器。该路由只接受封闭的 `WIRE_TOOLS` 名单、受限 JSON object、loopback 对端、稳定的调用方 session ID 与一个 call ID。Caddy 拒绝 `/internal/*`，因此该路由没有公开 HTTP 路径。

部署 MCP server 暴露全部十三个 DSH wire tool，使用相同 action 与 operation-kind 名单。它以 session ID `hermes-weixin` 把调用转发到 loopback 路由，使资源 candidate ID、计划归属与计划执行保持在同一个调用方 session。自用适配器以 `allowed-once` 回答审批请求；write mode、canonical 规划、目标重验证、审计记录与验证保持不变。

Hermes 加载八个 Emby 运营技能与一个路由技能。技能要求精确计数 action、先查询再规划的资源处理、每次 mutation 执行 `plan -> execute -> verify`，并要求 destructive 工作具有明确用户意图。

## Alternatives considered

**在 MCP server 中实现业务查询。** 不采用，因为直接 SQL 与临时 Emby 调用会绕过领域规则，且无法支持资源 candidate 或已验证写入。

**在 MCP 进程内运行第二套 EmbyMedia runtime。** 不采用，因为它会在活动 DSH Host 旁重复拥有凭据、任务、维护、缓存与生命周期。

**公开暴露 internal 路由。** 不采用，因为 MCP 与 DSH 运行在同一台 Debian 主机；公开 mutation API 会增加身份验证与网络攻击路径，而没有运营价值。

## Consequences

DSH 与 Hermes 返回相同结构化事实并共享计划状态。Hermes 无需审批卡即可完成完整运营流程，而 critical 与 high-risk 操作仍需 canonical 预览与 effect 后验证。MCP 适配器依赖本机 DSH Host 及其 loopback listener；停止该服务会使全部 MCP 调用 fail closed。
