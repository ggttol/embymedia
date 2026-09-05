# PRODUCT.md

## Product
EmbyMedia is a self-hosted, single-administrator operations system for Emby, CloudDrive2, 115 resources, STRM generation, metadata, tasks, audit, REST, and MCP. The V2 application runs as one Go binary with an embedded Vue interface.

## Audience and primary task
- Primary user: administrator `gaotao`.
- Primary task: understand media-system state, inspect libraries/resources/tasks, configure upstream services, and launch safe plan → approval → execute → verify workflows.

## Allowed visible capabilities
- Health and dependency status.
- Emby library inventory.
- Recent task, schedule, and audit state.
- Credential configured/writable status without revealing values.
- Non-secret endpoint/settings visibility.
- Agent integration through Streamable HTTP MCP, stdio MCP, and OpenAPI 3.1.
- Supported writes: 115 file operations and share transfer, Emby library refresh, settings, scheduled tasks, and Agent token management.

## Constraints and non-goals
- The public Web endpoint uses a login proxy; same-host Agent clients use the loopback MCP address.
- Never display stored secret values; Agent token plaintext appears only in the creation response.
- Unsupported operations are labelled unavailable; no fake success or inert action.
- No multi-user RBAC claim.
- MCP tool discovery proves registration, not successful execution against every upstream dependency.

## Missing facts
- No supplied product logo beyond the current EmbyMedia mark.
- No hardware-transcoding capability.
- CloudDrive remount, 115 share-link generation, and running-task cancellation are not implemented by the configured providers; their MCP tools return explicit errors.

## Working assumptions
- The same-host Debian deployment is the primary Agent runtime.
- Public browser access remains protected by the existing login proxy.
