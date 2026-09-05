---
name: embymedia-v2-operator
description: Use for EmbyMedia requests through Hermes to inspect or manage 115 files, Emby libraries, CloudDrive2 mounts, STRM files, and persistent tasks with the standalone MCP tools.
---

# EmbyMedia V2 operator

Use only the `mcp_embymedia_*` tools registered by the `embymedia` MCP server. The old `embymedia_plan`, `embymedia_execute`, `embymedia_verify`, and other thirteen-tool DSH names do not exist in V2.

## Read before writing

- Start dependency diagnosis with `mcp_embymedia_system_health`. A degraded result is evidence, not success.
- Read managed account IDs and directory CIDs through `mcp_embymedia_system_get_config`, `mcp_embymedia_c115_list_files`, or the Web console. Stored secrets are intentionally redacted.
- Search indexed 115 share resources with `mcp_embymedia_c115_search`. Search results are external index facts; they are not files already stored in the managed account.
- Read Emby libraries with `mcp_embymedia_emby_get_libraries`. Inspect one item only with its exact ID through `mcp_embymedia_emby_inspect_item`; never pass a title as an item ID.
- Read CloudDrive state with `mcp_embymedia_cd2_mount_status`. Report the returned filesystem status and error; never infer a mount from a configured path alone.

## Direct mutations

Use IDs returned by a fresh read in the same request. Never guess a file ID, CID, account ID, library ID, or Emby item ID.

- `mcp_embymedia_c115_mkdir`: `name`, optional `parent_cid`, optional `account_id`.
- `mcp_embymedia_c115_rename`: `file_id`, `new_name`, optional `account_id`.
- `mcp_embymedia_c115_move`: `file_id`, `target_cid`, optional `account_id`.
- `mcp_embymedia_c115_save_share`: `url`, optional `password`, `target_cid`, and `account_id`. Completion means the 115 provider accepted every returned top-level entry.
- `mcp_embymedia_c115_get_share_link`: `file_id` and optional `account_id`. This publishes the selected file or directory; call it only when the user explicitly asks to create a share.
- `mcp_embymedia_emby_refresh_library`: optional `library_id`; omission refreshes the complete Emby library inventory.
- `mcp_embymedia_cd2_remount`: call only after the user confirms active playback is stopped, with `confirm_playback_stopped=true`. It requires a CloudDrive2 API token, returns completed/failed mount steps, and may require an Emby restart plus media-canary verification before playback resumes.

V2 exposes no MCP deletion tool. File deletion remains behind the Web/REST destructive-action switch and explicit browser confirmation.

## Persistent tasks

`mcp_embymedia_task_submit` accepts exactly one of these task types and an object-valued `payload`:

- `emby_refresh`: `{}` or `{"library_id":"..."}`.
- `emby_match`: `{"item_id":"...","tmdb_id":"..."}`.
- `c115_save_share`: `{"url":"...","password":"...","target_cid":"...","account_id":"..."}`; optional fields may be omitted.
- `c115_offline_download`: `{"urls":["magnet:..."],"target_cid":"...","account_id":"..."}`.

- `strm_sync`: `{}` or `{"library":"relative/library/folder"}`; creates or updates STRM files from the configured media tree and verifies every generated target.
- `strm_verify`: `{}` or `{"library":"relative/library/folder"}`; checks existing STRM targets without changing files.
- `emby_missing_posters`: `{}`; returns up to 100 Movie or Series items that currently have no image and changes no Emby metadata.

After submission, poll `mcp_embymedia_task_query`. Only `completed` is success. `pending` and `running` are non-terminal; `failed` and `cancelled` require reporting the recorded error. Use `mcp_embymedia_task_get_logs` for durable attempts and log messages. Use `mcp_embymedia_task_cancel` once for pending or running work; never claim cancellation until a later query returns `cancelled`.

A service restart marks interrupted effectful work failed instead of replaying it. Review current 115, Emby, and CloudDrive facts before using the Web retry action.

## Security and reporting

Never ask for or print stored cookies, API keys, bearer tokens, webhook secrets, or extraction codes already present in a tool input. Preserve provider errors verbatim except secret-bearing values. Tool discovery proves registration only; report an operation as successful solely from that tool call's non-error result, and include task terminal state when the operation is asynchronous.
