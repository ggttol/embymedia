# PRODUCT.md

## Product
EmbyMedia is a self-hosted Go and Vue operations system for Emby, CloudDrive2, 115 resources, STRM generation, metadata, tasks, audit, REST, and MCP. The V2 application runs as one Go binary with an embedded Vue interface and a separate loopback login service.

## Audience and primary task
- Primary users: the system administrator and explicitly created media operators.
- Primary task: understand media-system state, inspect libraries/resources/tasks, configure upstream services, and execute bounded provider operations with explicit errors and durable audit records.

## Allowed visible capabilities
- Live dependency, 115 account, VIP, quota, CloudDrive mount, and filesystem status.
- Emby library inventory, exact item inspection, refresh, explicit TMDB matching, and missing-poster inspection.
- 115 file lifecycle, share receive/create, offline download, and indexed resource search.
- STRM synchronization and containment-aware target verification.
- Persistent task, schedule, execution-attempt, cancellation, retry, and audit state.
- Credential configured/writable status without revealing stored values.
- Agent integration through authenticated Streamable HTTP MCP, loopback legacy SSE, stdio MCP, and OpenAPI 3.1.
- Supported writes: bounded 115 and Emby operations, CloudDrive gRPC remount, settings, scheduled tasks, and Agent token management.
- Session-backed user administration: create users, enable or disable access, reset passwords, assign administrator or operator status, and terminate the current browser session.

## Constraints and non-goals
- The public Web endpoint uses a login proxy; public Agent calls use `X-Agent-Token`, while same-host Hermes also uses a token on the loopback MCP address.
- Never display stored password material or secret values; Agent token plaintext appears only in the creation response.
- Administrators and operators can use media operations; only administrators can manage browser users.
- Tool discovery is not execution evidence; the UI distinguishes discovery from successful or failed read calls.
- Provider operations fail with their actual configuration, authorization, transport, or response error; no synthetic success or empty fallback.

## Missing facts
- No supplied product logo beyond the current EmbyMedia mark.
- No hardware-transcoding capability.
- Missing-poster inspection and explicit TMDB-ID application are implemented; automatic TMDB candidate selection remains intentionally absent because ambiguous matches require an operator choice.

## Working assumptions
- The same-host Debian deployment is the primary Agent runtime.
- Public browser access remains protected by the existing login proxy.
- CloudDrive container restarts require an Emby restart and media-canary verification before playback resumes.
