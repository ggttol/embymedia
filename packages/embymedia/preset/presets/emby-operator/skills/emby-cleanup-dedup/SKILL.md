---
name: emby-cleanup-dedup
description: Use for duplicate analysis, delete or replace, media moves, empty-folder cleanup, cleanup workbenches, and undo.
---

# Cleanup and deduplication

Treat the latest user request as an independent operation. Do not reuse item IDs, plan IDs, errors, or conclusions from earlier requests unless the user explicitly names them.

For ordinary deletion, use one bounded path: resolve matching Emby items with `embymedia_library list_items`, ask once only when the match or requested scope is ambiguous, create one `media.delete` plan containing every selected item below each shared root, then call `embymedia_execute`. The Host groups Movie, Video, and Series items by STRM/115 root, removes each root once, handles Emby refresh fallback, and verifies Emby/STRM/115 absence. Do not call analyze, audit, resource, gaps, or `list_strm` to discover a deletion schema. Do not create `library.scan` as deletion recovery.

Use `dedup.delete` only when the user explicitly wants to retain one keeper from a duplicate same-TMDB group. Full removal always uses `media.delete`; it never invents a keeper.

Before the first plan, use at most two lookup calls. Repeating the same failure twice ends the attempt with the exact blocker. A partial result permits one owner-bound `retryPlanId` plan and no alternate workflow. A terminal `done`, `partial`, `failed`, or `cancelled` result ends the task immediately; report the per-target facts and do not continue into recommendations or another user request.

Deletion is critical and irreversible. The plan enumerates at most 100 explicit Emby items and every shared storage root. The Host owns approval, execution order, partial accounting, refresh fallback, and independent verification.
