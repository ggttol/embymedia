# Agent Note: EmbyMedia Go MCP shares the Web port

Status: implemented

[English](2026-09-05-embymedia-go-mcp-web-port.md) | 中文

## Problem

独立 Go 服务在 3080 端口暴露浏览器与 REST API，却只在 3081 端口以 SSE 暴露 MCP。部署代理保护 3080，但不发布 3081，因此 Agent 页面展示了外部客户端无法访问的地址。Debian Hermes 仍使用早期由 DSH 支撑的 stdio 适配器，并发现另一组十三个工具。

## Decision

Go MCP registry 通过主 Web listener 上的 `/mcp` 提供 Streamable HTTP。除非请求携带已认证浏览器 identity，否则 Go HTTP wrapper 会在 MCP 初始化前要求 Agent token；工具调用随后执行读写 scope 与共享限流桶检查。相同二进制支持受信本机 `-mcp` stdio 客户端；3081 端口保留为需要 token 且仅限同主机的 SSE listener。Agent 页面执行真实的 `initialize`、`tools/list` 与安全只读工具交换，并区分 discovery 与执行结果。

Debian 主机上的 Hermes 携带 `X-Agent-Token` 连接 `http://127.0.0.1:3080/mcp`。部署配置以 `embymedia` 名称使用独立 Go registry，并发现三十四个工具。

Agent token 创建接口只返回一次随机明文，数据库仅存储其 SHA-256 摘要。REST 与 HTTP MCP 请求通过同一个进程级 authorizer 执行身份验证、限流和读写 scope 检查；无效凭据 fail closed。自主运行令牌默认具有读写权限和每分钟 120 次请求额度。浏览器请求继续由部署登录代理保护。

## Alternatives considered

**发布 3081 端口。** 不采用，因为第二个公开 listener 会扩大部署与防火墙配置，而 Streamable HTTP 可以复用受保护的应用 listener。

**保留由 DSH 支撑的 Hermes 适配器。** 不采用，因为它会保留第二套 runtime 与不同工具名单；独立 release 会移除该适配器及其部署服务。

**展示静态绿色状态。** 不采用，因为路由存在并不能证明 MCP 握手或工具发现成功。

## Consequences

一个应用端口提供 Web UI、REST schema 与首选 MCP transport。同主机 Hermes 与公网 Agent 客户端使用相同的三十四工具 registry 和 token 策略。工具 discovery 表示协议注册，而非上游执行成功；provider 配置与 runtime 失败会返回明确 MCP 错误，而非合成成功。Caddy 把携带 token 的 Agent 路径直接转发到 fail-closed Go middleware，并把不含该 header 的浏览器流量保留在登录保护后。
