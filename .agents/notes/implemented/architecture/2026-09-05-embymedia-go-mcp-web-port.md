# Agent Note: EmbyMedia Go MCP shares the Web port

Status: implemented

English | [中文](2026-09-05-embymedia-go-mcp-web-port.zh.md)

## Problem

The standalone Go service exposed its browser and REST API on port 3080 but exposed MCP only as SSE on port 3081. The deployment proxy protects port 3080 and does not publish port 3081, so the Agent page advertised an address that external clients could not reach. Debian Hermes still used the earlier DSH-backed stdio adapter and discovered thirteen different tools.

## Decision

The Go MCP registry is available through Streamable HTTP at `/mcp` on the main Web listener. The Go HTTP wrapper requires an Agent token before MCP initialization unless the request carries the authenticated browser identity; tool calls then enforce read/write scope and the shared rate bucket. The same binary supports trusted local `-mcp` stdio clients, while port 3081 remains a token-authenticated, on-host SSE listener. The Agent page performs a real `initialize`, `tools/list`, and safe read-tool exchange and distinguishes discovery from execution results.

Hermes on the Debian host connects to `http://127.0.0.1:3080/mcp` with `X-Agent-Token`. The deployed configuration uses the standalone Go registry under the `embymedia` name and discovers eighteen tools.

Agent token creation returns a random secret once and stores only its SHA-256 digest. REST and HTTP MCP requests authenticate, rate-limit, and check read/write scope through one process-wide authorizer; invalid credentials fail closed. Browser requests continue through the deployment login proxy.

## Alternatives considered

**Publish port 3081.** Rejected because a second public listener expands deployment and firewall configuration while Streamable HTTP can share the protected application listener.

**Keep the DSH-backed Hermes adapter.** Rejected because it retains a second runtime and a different tool roster; the standalone release removes the adapter and its deployment service.

**Display static green status.** Rejected because route presence does not prove an MCP handshake or tool discovery.

## Consequences

One application port serves the Web UI, REST schema, and preferred MCP transport. Same-host Hermes has a tested eighteen-tool connection and uses the same token policy as public Agent clients. Tool discovery means protocol registration, not successful upstream execution; provider configuration and runtime failures return explicit MCP errors instead of synthetic success. Caddy forwards token-bearing Agent paths directly to the fail-closed Go middleware and keeps headerless browser traffic behind login.
