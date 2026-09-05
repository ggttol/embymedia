---
name: embymedia-v2-operator
description: Use for EmbyMedia requests through Hermes to inspect or manage 115 files, Emby libraries, CloudDrive2 mounts, STRM files, and persistent tasks with the standalone MCP tools.
---

# EmbyMedia V2 operator

Use only the `mcp_embymedia_*` tools registered by the `embymedia` MCP server. The old `embymedia_plan`, `embymedia_execute`, `embymedia_verify`, and other thirteen-tool DSH names do not exist in V2.

## Read before writing

- Start dependency diagnosis with `mcp_embymedia_system_health`. A degraded result is evidence, not success.
- Discover managed account IDs with `mcp_embymedia_c115_list_accounts`, browse directory CIDs with `mcp_embymedia_c115_list_files`, and search already stored content with `mcp_embymedia_c115_search_files`. Stored secrets are never returned.
- Search indexed external shares with `mcp_embymedia_c115_search`; inspect a selected share with `mcp_embymedia_c115_snapshot_share` before saving it. Search results are not files already stored in the managed account.
- Read Emby libraries with `mcp_embymedia_emby_get_libraries`. Use `mcp_embymedia_emby_search_items` to resolve titles to exact IDs, then `mcp_embymedia_emby_inspect_item` for one selected result. Never pass a title as an item ID.
- Read current playback with `mcp_embymedia_emby_list_sessions` before disruptive maintenance. Read CloudDrive state with `mcp_embymedia_cd2_mount_status`; report the returned filesystem status and error.
- Use `mcp_embymedia_task_list` to discover recent task IDs and `mcp_embymedia_schedule_list` to inspect automation before changing it.

## Direct mutations

Use IDs returned by a fresh read in the same request. Never guess a file ID, CID, account ID, library ID, or Emby item ID.

- `mcp_embymedia_c115_mkdir`: `name`, optional `parent_cid`, optional `account_id`.
- `mcp_embymedia_c115_rename`: `file_id`, `new_name`, optional `account_id`.
- `mcp_embymedia_c115_move`: `file_id`, `target_cid`, optional `account_id`.
- `mcp_embymedia_c115_save_share`: `url`, optional `password`, `target_cid`, and `account_id`. Completion means the 115 provider accepted every returned top-level entry.
- `mcp_embymedia_c115_get_share_link`: `file_id` and optional `account_id`. This publishes the selected file or directory; call it only when the user explicitly asks to create a share.
- `mcp_embymedia_emby_refresh_library`: optional `library_id`; omission refreshes the complete Emby library inventory.
- `mcp_embymedia_cd2_remount`: checks live Emby playback itself and refuses to disrupt an active session. When idle, it returns completed or failed mount steps and may require an Emby restart plus media-canary verification before playback resumes.

For 115 deletion, first call `mcp_embymedia_c115_request_delete` with one freshly listed `parent_cid` and the exact `file_ids`. Tell the user that the request is waiting in `/agent`. Only after the user approves those displayed names may `mcp_embymedia_c115_execute_delete` consume the returned `approval_id`. Approval expires after 15 minutes, is bound to one account and exact IDs, and cannot be replayed. Never use REST settings or file deletion to bypass this flow. Emby library deletion is not exposed.

## Persistent tasks

`mcp_embymedia_task_submit` accepts exactly one of these task types and an object-valued `payload`:

- `emby_refresh`: `{}` or `{"library_id":"..."}`.
- `emby_match`: `{"item_id":"...","tmdb_id":"..."}`.
- `c115_save_share`: `{"url":"...","password":"...","target_cid":"...","account_id":"..."}`; optional fields may be omitted.
- `c115_offline_download`: `{"urls":["magnet:..."],"target_cid":"...","account_id":"..."}`.

- `strm_sync`: `{}` or `{"library":"relative/library/folder"}`; creates or updates STRM files from the configured media tree and verifies every generated target.
- `strm_verify`: `{}` or `{"library":"relative/library/folder"}`; checks existing STRM targets without changing files.
- `emby_missing_posters`: `{}`; returns up to 100 Movie or Series items that currently have no image and changes no Emby metadata.

Use `mcp_embymedia_c115_list_offline` to inspect provider-side offline progress. Use `mcp_embymedia_emby_missing_posters` for a direct bounded missing-artwork inventory when a persistent task is unnecessary.

After submission, poll `mcp_embymedia_task_query`. Only `completed` is success. Use `mcp_embymedia_task_get_logs` for durable attempts and `mcp_embymedia_task_cancel` for pending or running work. Use `mcp_embymedia_task_retry` only after reading a failed or cancelled task and checking the provider state; it creates a new task instead of replaying the old record.

A service restart marks interrupted effectful work failed instead of replaying it. Review current 115, Emby, and CloudDrive facts before retrying.

## Automatic schedules

Use `mcp_embymedia_schedule_list` before mutations. `mcp_embymedia_schedule_upsert` creates or updates a schedule with `name`, `task_type`, `enabled`, optional six-field `cron_expr`, and task `payload`; pass `schedule_id` to update. `mcp_embymedia_schedule_run` creates one persistent task immediately. `mcp_embymedia_schedule_delete` removes only the schedule, never task history or media.

Use `mcp_embymedia_system_update_config` for validated non-destructive settings changes. It cannot change the browser deletion switch. Read the redacted result and component health after updating; never replace an existing secret with an empty value.

## Security and reporting

Never ask for or print stored cookies, API keys, bearer tokens, webhook secrets, or extraction codes already present in a tool input. Preserve provider errors verbatim except secret-bearing values. Tool discovery proves registration only; report an operation as successful solely from that tool call's non-error result, and include task terminal state when the operation is asynchronous.
