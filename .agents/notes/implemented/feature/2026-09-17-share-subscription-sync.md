# Share subscription and selective episode import

## Decision
A Quark target directory is where a share is saved, not a filter for downloading existing files. Repeatedly creating empty directories and submitting the original URL still imports the whole share. Selection must be explicit and validated before provider writes and again before downloading bytes.

## Manual selection
REST and MCP accept a non-empty `selected_source_manifest` containing live share leaf IDs, revisions, names, and sizes. The service derives internal source IDs; clients cannot supply internal destination bindings. The browser recursively previews files, starts with no selection, and displays selected counts and bytes.
Whole-pack import requires explicit `import_all:true`. Missing selection, stale identity, unexpected saved files, and ambiguous saved roots fail closed. Persisted tasks without an explicit mode do not resume as whole-pack downloads.

## Missing-episode completion
`series_auto_fill` accepts `source_shares` keyed by exact Emby Series ID, with provider, HTTPS URL, and optional password. Supplied shares bypass search-index availability, not canonical identity or aired-gap checks. Without `series_ids`, only supplied Series keys are processed.
Only matching aired missing episodes become selected leaf manifests. Existing and future episodes are not downloaded. Unknown Series IDs and unmatched resource overrides are visible errors/findings, not empty success or fallback to a whole pack.
Expired ordinary 115 shares remain per-Series findings, preserving valid queued Quark work for other Series. Cancellation, authorization, rate limiting, systemic transport errors, identity changes, and post-write verification failures remain failures.

## Subscription execution
`share_auto_sync` loads an active subscription and resolves its 115 `target_cid` to exactly one existing TMDB-bound Series in a following library. It enqueues the same scoped `series_auto_fill` workflow and returns `next_task_id` with `verification_complete:false`.
Repeated polling reuses in-flight subscription work. The subscription never saves all share leaves first, never writes to the Quark root as a placeholder, and never claims queued work is complete. Unsupported or ambiguous destination bindings fail before provider writes.
`last_cursor_time` is retained in storage but is not used to infer episode ownership or claim incrementality. Emby's aired-gap inventory is the source of missing episodes; upstream metadata delay can still delay automatic discovery. A schedule must explicitly enqueue `share_auto_sync` with a subscription ID; creating a subscription alone does not create a scheduler.

## Transfer and recovery
Use the durable transfer pipeline, not ad-hoc CDN writes into final media paths. Debian and NAS downloads recover expired Quark CDN credentials; returned ranges and lengths must match the request. Complete source bytes are hashed and 115 identity is verified before ingestion.
Retry may save again only when no prior import row exists, proving failure occurred before share-save ownership. An existing row without reconciled roots is ambiguous and requires review. A STRM file or Emby media-source count alone does not prove a complete playable video.

## Verification
Local provider fixtures cover selective E17/E18 manifests from a mixed share, rejection of unexpected old files, expired-share isolation, subscription work reuse, preflight versus ambiguous retries, CDN range validation, and concurrent NAS credential refresh. Browser checks use labelled local fixtures, not production credentials or media.
