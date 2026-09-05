# Agent Note: EmbyMedia Go MCP shares the Web port

Status: implemented

English | [中文](2026-09-05-embymedia-go-mcp-web-port.zh.md)

## Problem

The standalone Go service exposed its browser and REST API on port 3080 but exposed MCP only as SSE on port 3081. The deployment proxy protects port 3080 and does not publish port 3081, so the Agent page advertised an address that external clients could not reach. Debian Hermes still used the earlier DSH-backed stdio adapter and discovered thirteen different tools.

## Decision

The Go MCP registry is available through Streamable HTTP at `/mcp` on the main Web listener. The deployment proxy authenticates public requests and forwards the original Host header, so the embedded MCP handler disables its loopback-only Host check while the proxy remains the public security boundary. The same binary supports `-mcp` for stdio clients, while port 3081 remains a legacy on-host SSE listener. The Agent page performs a real `initialize` and `tools/list` exchange and derives its tool status from the response instead of displaying a fixed availability claim.

Hermes on the Debian host connects to `http://127.0.0.1:3080/mcp`. This loopback route avoids the login proxy and the unpublished SSE port. The deployed Hermes configuration uses the standalone Go registry under the `embymedia` name and discovers eighteen tools.

Agent token creation returns a random secret once and stores only its SHA-256 digest. REST requests that present `X-Agent-Token` or a Bearer token are authenticated, rate-limited, and checked for write scope; invalid credentials fail closed. Browser requests continue through the deployment login proxy.

## Alternatives considered

**Publish port 3081.** Rejected because a second public listener expands deployment and firewall configuration while Streamable HTTP can share the protected application listener.

**Keep the DSH-backed Hermes adapter.** Rejected for the standalone V2 deployment because it retains a second runtime and a different tool roster. The earlier adapter remains relevant only to the DSH application path documented in the superseded note.

**Display static green status.** Rejected because route presence does not prove an MCP handshake or tool discovery.

## Consequences

One application port serves the Web UI, REST schema, and preferred MCP transport. Same-host Hermes avoids public-network authentication and has a tested eighteen-tool connection. Tool discovery means protocol registration, not successful upstream execution; actions without a configured implementation return explicit MCP errors instead of synthetic success. External MCP clients still pass through the deployment login proxy; the UI does not claim that the plain HTTP public hostname is a generally usable unauthenticated MCP endpoint.
