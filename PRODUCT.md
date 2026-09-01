# PRODUCT.md

## Product
EmbyMedia is a self-hosted, single-administrator operations system for Emby, CloudDrive2, 115 resources, STRM generation, metadata, users, tasks, audit, and approval-controlled writes. DSH remains the conversational execution and approval engine.

## Audience and primary task
- Primary user: administrator `gaotao`.
- Primary task: understand media-system state, inspect libraries/resources/tasks, configure upstream services, and launch safe plan → approval → execute → verify workflows.

## Allowed visible capabilities
- Health and dependency status.
- Emby library inventory.
- Recent task, schedule, and audit state.
- Credential configured/writable status without revealing values.
- Non-secret endpoint/settings visibility.
- AI conversation handoff for supported writes.
- Supported writes: library create/scan, 115 resource add-new, poster/metadata actions, Emby user actions, config update, Smart Action policy/dismiss.

## Constraints and non-goals
- HTTP self-use mode is an accepted deployment choice; do not claim public-Internet security.
- Never display stored secret values.
- Unsupported operations are labelled unavailable; no fake success or inert action.
- Scheduler remains disabled.
- No multi-user RBAC claim.
- DSH chat and approval UI remain authoritative for business mutations.

## Missing facts
- No supplied product logo beyond current DSH/Emby marks.
- No hardware-transcoding capability.
- No verified CloudDrive update or 115 Open API integration.

## Working assumptions
- The workspace may poll a read-only Host Remote for current facts.
- Action buttons can route a structured prompt to the current DSH session instead of bypassing approval.
