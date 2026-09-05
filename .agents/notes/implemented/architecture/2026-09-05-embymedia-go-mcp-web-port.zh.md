# Agent Note: EmbyMedia Go MCP shares the Web port

Status: implemented

[English](2026-09-05-embymedia-go-mcp-web-port.md) | 中文

## Problem

独立 Go 服务在 3080 端口暴露浏览器与 REST API，却只在 3081 端口以 SSE 暴露 MCP。部署代理保护 3080，但不发布 3081，因此 Agent 页面展示了外部客户端无法访问的地址。Debian Hermes 仍使用早期由 DSH 支撑的 stdio 适配器，并发现另一组十三个工具。

## Decision

Go MCP registry 通过主 Web listener 上的 `/mcp` 提供 Streamable HTTP。部署代理验证公开请求并转发原始 Host header，因此嵌入式 MCP handler 关闭仅限 loopback 的 Host 检查，代理继续作为公开安全边界。相同二进制支持面向 stdio 客户端的 `-mcp`；3081 端口保留为旧式、仅限同主机的 SSE listener。Agent 页面执行真实的 `initialize` 与 `tools/list` 交换，并从响应推导工具状态，不再展示固定的可用性声明。

Debian 主机上的 Hermes 连接 `http://127.0.0.1:3080/mcp`。该 loopback 路由绕过登录代理与未发布的 SSE 端口。部署中的 Hermes 配置以 `embymedia` 名称使用独立 Go registry，并发现十八个工具。

Agent token 创建接口只返回一次随机明文，数据库仅存储其 SHA-256 摘要。携带 `X-Agent-Token` 或 Bearer token 的 REST 请求会经过身份验证、限流与写权限检查；无效凭据 fail closed。浏览器请求继续由部署登录代理保护。

## Alternatives considered

**发布 3081 端口。** 不采用，因为第二个公开 listener 会扩大部署与防火墙配置，而 Streamable HTTP 可以复用受保护的应用 listener。

**保留由 DSH 支撑的 Hermes 适配器。** 独立 V2 部署不采用，因为它保留第二套 runtime 与不同的工具名单。早期适配器只对已被取代的 Agent Note 所记录的 DSH 应用路径仍有意义。

**展示静态绿色状态。** 不采用，因为路由存在并不能证明 MCP 握手或工具发现成功。

## Consequences

一个应用端口提供 Web UI、REST schema 与首选 MCP transport。同主机 Hermes 无需经过公网身份验证，并具有经过验证的十八工具连接。工具发现表示协议注册，而非上游执行成功；缺少已配置实现的操作会返回明确的 MCP 错误，而非合成成功结果。外部 MCP 客户端仍会经过部署登录代理；UI 不会宣称纯 HTTP 公网主机名是通用、无需身份验证的 MCP endpoint。
