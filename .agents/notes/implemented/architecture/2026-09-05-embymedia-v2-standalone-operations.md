# Agent Note: standalone V2 owns EmbyMedia operations

Status: implemented

English | [中文](2026-09-05-embymedia-v2-standalone-operations.zh.md)

## Problem

The standalone service registered eighteen MCP tools, but registration hid incomplete behavior: CloudDrive status used invented capacity, remount and 115 share creation were unavailable, item inspection searched by title, task handlers simulated work, OpenAPI omitted routes, and Hermes loaded skills for a different DSH tool registry. The deployed DSH service also remained in a restart loop beside the Go service.

## Decision

The Go binary is the only deployed EmbyMedia application runtime. It serves Vue, REST, OpenAPI 3.1, Streamable HTTP MCP, legacy loopback SSE, and stdio MCP. Debian Hermes connects directly to its loopback `/mcp` endpoint and loads one V2 skill for the thirty-four registered tools.

Provider tools execute provider operations. The 115 client lists, creates, renames, moves, deletes, saves shares, creates share links, and submits offline downloads with the selected account credential. The CloudDrive2 client uses the version-matched gRPC methods for system state, mount inventory, unmount, and mount; filesystem capacity comes from `statfs`. CloudDrive2 reports container paths, while the configured mount path identifies the corresponding host filesystem path used for health checks. Emby item inspection addresses one exact item ID, metadata application checks the upstream response, and every Emby request sends its API key in `X-Emby-Token` rather than a URL that can appear in durable errors.

The persistent task queue accepts only its published task types: Emby refresh and match, 115 share and offline operations, and STRM synchronization or verification. Every scheduled launch atomically creates a linked execution and updates the schedule's latest execution ID and launch time. Each attempt durably records start and completion timestamps, progress, result, error, and logs. A full Emby refresh starts the `RefreshLibrary` scheduled task, follows its provider-reported progress, and finishes only after Emby records a terminal result; a specific-library item refresh records only request acceptance because that endpoint exposes no completion state. Cancellation stops a full scan started by the execution. Restarted effectful work becomes failed and requires an explicit retry, because automatic replay can duplicate an accepted upstream write. The scheduler computes and persists the first next-run time from the parsed cron schedule instead of reading the zero `cron.Entry.Next` value before the cron loop starts.

REST and MCP share token authentication, read/write scopes, per-token rate buckets, last-use updates, and secret-redacted audit records. Autonomous tokens default to read/write access and 120 requests per minute. Non-destructive tools can discover accounts, shares, offline work, Emby items and sessions, persistent tasks, and schedules before mutating them. A 115 deletion uses two tools: the first binds freshly resolved names and IDs into a 15-minute pending request; an authenticated browser user approves that exact request; the second consumes it once. Agent tokens cannot approve requests, enable the browser deletion switch, or use the REST file-delete route. Emby library deletion is not exposed. OpenAPI lists every registered API operation with unique operation IDs and request schemas. CloudDrive remount checks live Emby sessions itself and refuses active playback.

The loopback login service owns browser users, password hashes, signed sessions, and the administrator-only user API. Administrators and operators share media-operation access; only administrators can create, disable, reset, or delete browser users. Password changes rotate the user's session version, and the service keeps at least one enabled administrator. Caddy exposes only the login API and forwards the authenticated username and role to the application.

Browser status distinguishes untested, checking, successful, failed, and stale observations; MCP discovery is not execution success. The visible Agent page refreshes connection discovery every 30 seconds and the latest 100 audit records every 10 seconds, then attributes each tool's latest persisted outcome to its caller and timestamp. It pauses polling while hidden and does not repeat provider read calls during lightweight connection refreshes. Transfer pages share an explicit destination and refuse an unavailable saved CID rather than silently choosing the root. Clipboard success requires a completed write; insecure HTTP and denied clipboard access use manual selection. These distinctions prevent interface feedback from claiming unobserved reads or writes.

Audit queries aggregate all matching records in one SQLite read transaction before pagination. Agent identity is the authenticated token name, not a guessed client brand; tool inputs and bounded outputs cannot reconstruct user instructions or model reasoning. Database ownership is exclusive across HTTP, stdio, and maintenance processes, because independent task recovery and schedulers can otherwise duplicate accepted operations. Cron callbacks validate their registration under the same lock as deletion and replacement, so stale callbacks cannot resurrect schedules.

Public Agent proxy routes discard caller-supplied browser identity headers. Fetch metadata is not authentication, and explicit invalid credentials cannot fall back to browser trust. Login issuance rechecks the password and session version after password hashing; user updates publish only after successful persistence. Deployment recovery preserves the active release and installed configurations, and backups include the live browser-user database. Provider file discovery preserves exact file IDs; CloudDrive host mapping requires complete path correspondence rather than matching basenames.

## Alternatives considered

**Keep DSH as the production dispatcher.** Rejected because the V2 deployment would retain Node, Cordis, a second tool vocabulary, and a failing service beside the standalone binary.

**Keep registered tools that return unavailable errors.** Rejected because discovery would continue to overstate executable capability; provider prerequisites may still fail, but every tool now has a real implementation path.

**Automatically replay running tasks after restart.** Rejected because 115 and Emby may have accepted an operation before the local process lost its terminal update. Explicit retry preserves the unknown-side-effect warning.

## Consequences

The production runtime no longer needs the DSH service, Node MCP bridge, Cordis deployment patch, PostgreSQL service, or Node control helper. STRM tasks require explicit media, output, and Emby path settings. Discovery and execution evidence remain different facts, so the Agent page reports them separately. Most operations run with one full-access token and no per-action approval; only media deletion crosses the target-bound browser decision point. Task and schedule tools expose typed discovery and mutations instead of requiring agents to guess IDs or use the browser.
