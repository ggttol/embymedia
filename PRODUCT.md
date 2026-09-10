# PRODUCT.md

## Product
EmbyMedia 2.1.0 is a self-hosted Go and Vue operations system for Emby, CloudDrive2, 115 resources, STRM generation, metadata, tasks, audit, REST, and MCP. The application runs as one Go binary with an embedded Vue interface and a separate loopback login service.

## Audience and primary task
- Primary users: the system administrator and explicitly created media operators.
- Primary task: understand media-system state, inspect libraries/resources/tasks, configure upstream services, and execute bounded provider operations with explicit errors and durable audit records.

## Allowed visible capabilities
- Live dependency, 115 account, VIP, quota, CloudDrive mount, and filesystem status.
- Emby library inventory, exact item inspection, refresh, explicit TMDB matching, scheduled metadata repair with unique title/year/type confidence, and poster repair with verified remaining findings.
- 115 file lifecycle, share receive/create, offline download, and indexed resource search.
- STRM synchronization and containment-aware target verification.
- Persistent task, schedule, execution-attempt, cancellation, retry, and audit state, including automatic aired-episode completion restricted to the `电视剧追更` and `综艺追更` libraries.
- Credential configured/writable status without revealing stored values.
- Agent integration through authenticated Streamable HTTP MCP, loopback legacy SSE, stdio MCP, and OpenAPI 3.1. Thirty-four MCP tools expose account/stored-file/share/offline discovery, Emby item/session discovery, task and schedule lifecycle, provider writes, validated settings updates, and system health.
- Autonomous tokens default to read/write access and 120 requests per minute. Non-destructive 115, Emby, CloudDrive, STRM, task, settings, schedule, and Agent-token operations do not require per-action confirmation.
- Session-backed user administration: create users, enable or disable access, reset passwords, assign administrator or operator status, and terminate the current browser session.

## Constraints and non-goals
- The public Web endpoint uses a login proxy; public Agent calls use `X-Agent-Token`, while same-host Hermes also uses a token on the loopback MCP address.
- Never display stored password material or secret values; Agent token plaintext appears only in the creation response.
- Administrators and operators can use media operations; only administrators can manage browser users.
- Tool discovery is not execution evidence; the UI distinguishes discovery from successful or failed read calls.
- Browser and Agent-initiated 115 deletion requires a short-lived request bound to freshly resolved IDs and names, followed by one authenticated browser decision and one non-replayable execution. The explicit `replace_completed_pack` task option is the sole automatic exception: with dangerous actions enabled, it may recycle an old Series root only after a transferred replacement owns every expected aired episode under the same unique TMDB identity. Emby library deletion is not exposed.
- Provider operations fail with their actual configuration, authorization, transport, or response error; no synthetic success or empty fallback.
- UI improvements retain the current routes, provider operations, Chinese interface, Warm Paper palette, and login proxy; they do not introduce dark mode, TLS changes, or synthetic operational success.
- Transfer destinations use a consistent browser preference; resource return navigation preserves search context. Favorites removal supports undo within the browser.
- Automatic episode completion requires one unique TMDB-bound Emby Series and one exact same-name 115 Series folder; ambiguous identity, future or unnumbered episodes, unsupported filenames, unavailable candidates, and unverified post-scan gaps remain visible findings. Completed-pack replacement stages a distinct root and keeps the old root unless the new Series passes full episode, path, and TMDB verification.

## Missing facts
- No supplied product logo beyond the current EmbyMedia mark.
- No hardware-transcoding capability.
- Automatic TMDB selection is limited to one high-confidence candidate whose normalized title, production year, and media type agree and whose TMDB ID does not collide with another item; ambiguous, duplicate, wrong-type, and unmatched items require operator review.

## Working assumptions
- The same-host Debian deployment is the primary Agent runtime.
- Public browser access remains protected by the existing login proxy.
- CloudDrive container restarts require an Emby restart and media-canary verification before playback resumes.
- The approved media catalog keeps the existing 115 `emby` root and aligns its immediate library names with generated STRM roots and Emby libraries. `电视剧追更` and `综艺追更` remain first-class Emby libraries; no family-video library is created. High-bitrate films use a distinct `IMAX巨幕` library, while uncertain identities remain outside Emby until reviewed.
