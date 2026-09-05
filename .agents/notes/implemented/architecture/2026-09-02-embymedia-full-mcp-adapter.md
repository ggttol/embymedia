# Agent Note: EmbyMedia tools keep one application path across DSH and MCP

Status: implemented

English | [中文](2026-09-02-embymedia-full-mcp-adapter.zh.md)

## Problem

EmbyMedia operators use both the DSH browser and a Hermes messaging gateway. A second implementation that queries PostgreSQL or Emby directly cannot preserve the package's resource candidates, canonical plans, target revalidation, execution audit, and independent verification. The two clients would report different facts and apply different write safeguards.

## Decision

The EmbyMedia Host owns one tool dispatcher. DSH `/tools` calls it in process. Trusted same-host adapters call the same dispatcher through `POST /internal/embymedia/tool` on the loopback web listener. The route accepts only the closed `WIRE_TOOLS` roster, bounded JSON objects, loopback peers, a stable caller session ID, and one call ID. Caddy rejects `/internal/*`, so the route has no public HTTP path.

The deployment MCP server exposes all thirteen DSH wire tools with the same action and operation-kind rosters. It forwards calls to the loopback route with session ID `hermes-weixin`, which keeps resource candidate IDs, plan ownership, and plan execution in one caller session. The self-use adapter answers approval requests with `allowed-once`; write mode, canonical planning, target revalidation, audit records, and verification remain unchanged.

Hermes loads the eight Emby operator skills plus one routing skill. The skills require exact count actions, query-before-plan resource handling, `plan -> execute -> verify` for every mutation, and explicit user intent for destructive work.

The standalone V2 deployment replaces this Hermes route with the Go-owned Streamable HTTP registry described in [EmbyMedia Go MCP shares the Web port](2026-09-05-embymedia-go-mcp-web-port.md). This DSH adapter remains the authority only for deployments that still use the DSH application path.

## Alternatives considered

**Implement business queries in the MCP server.** Rejected because direct SQL and ad hoc Emby calls bypass domain rules and cannot support resource candidates or verified writes.

**Run a second EmbyMedia runtime inside the MCP process.** Rejected because it would duplicate credential, job, maintenance, cache, and lifecycle ownership beside the active DSH Host.

**Expose the internal route publicly.** Rejected because MCP runs on the same Debian host; a public mutation API would add authentication and network attack paths without operational value.

## Consequences

DSH and Hermes return the same structured facts and share plan state. Hermes can perform the full operator workflow without approval cards, while critical and high-risk operations still require a canonical preview and post-effect verification. The MCP adapter depends on the local DSH Host and its loopback listener; stopping that service makes all MCP calls fail closed.
