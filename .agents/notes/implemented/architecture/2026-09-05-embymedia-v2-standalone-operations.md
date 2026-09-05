# Agent Note: standalone V2 owns EmbyMedia operations

Status: implemented

English | [中文](2026-09-05-embymedia-v2-standalone-operations.zh.md)

## Problem

The standalone service registered eighteen MCP tools, but registration hid incomplete behavior: CloudDrive status used invented capacity, remount and 115 share creation were unavailable, item inspection searched by title, task handlers simulated work, OpenAPI omitted routes, and Hermes loaded skills for a different DSH tool registry. The deployed DSH service also remained in a restart loop beside the Go service.

## Decision

The Go binary is the only deployed EmbyMedia application runtime. It serves Vue, REST, OpenAPI 3.1, Streamable HTTP MCP, legacy loopback SSE, and stdio MCP. Debian Hermes connects directly to its loopback `/mcp` endpoint and loads one V2 skill whose instructions name only the eighteen registered tools.

Provider tools execute provider operations. The 115 client lists, creates, renames, moves, deletes, saves shares, creates share links, and submits offline downloads with the selected account credential. The CloudDrive2 client uses the version-matched gRPC methods for system state, mount inventory, unmount, and mount; filesystem capacity comes from `statfs`. Emby item inspection addresses one exact item ID, and metadata application checks the upstream response.

The persistent task queue accepts only its published task types: Emby refresh and match, 115 share and offline operations, and STRM synchronization or verification. Each attempt has durable status, progress, result, error, and logs. Cancellation propagates through request contexts. Restarted effectful work becomes failed and requires an explicit retry, because automatic replay can duplicate an accepted upstream write.

REST and MCP share token authentication, read/write scopes, per-token rate buckets, last-use updates, and secret-redacted audit records. The file-delete route also requires the persisted destructive-action switch. OpenAPI lists every registered API operation with unique operation IDs and request schemas. CloudDrive webhook delivery uses its own secret and debounces file changes into one real Emby refresh task.

## Alternatives considered

**Keep DSH as the production dispatcher.** Rejected because the V2 deployment would retain Node, Cordis, a second tool vocabulary, and a failing service beside the standalone binary.

**Keep registered tools that return unavailable errors.** Rejected because discovery would continue to overstate executable capability; provider prerequisites may still fail, but every tool now has a real implementation path.

**Automatically replay running tasks after restart.** Rejected because 115 and Emby may have accepted an operation before the local process lost its terminal update. Explicit retry preserves the unknown-side-effect warning.

## Consequences

The production runtime no longer needs the DSH service, Node MCP bridge, Cordis deployment patch, PostgreSQL service, or Node control helper. CloudDrive remount requires an API token with mount permissions; STRM tasks require explicit media, output, and Emby path settings. Tool discovery remains distinct from execution evidence, so the Agent page performs safe read calls and labels discovered-only tools separately. The task center presents provider operations as user workflows with typed fields and readable schedules; raw task tags and JSON remain API and persistence data.
