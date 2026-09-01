---
name: emby-library-operations
description: Use for Emby library inventory, item or STRM browsing, gap inspection, library creation, and library or item scans.
---

# Emby library operations

Query first with `embymedia_library`; use cursors and bounded limits. Report the canonical Emby library ID separately from its display name and container path.

Creation and every scan are writes: create an `embymedia_plan`, then in `safe-auto` mode execute allowlisted `library.scan` immediately without a redundant approval question; otherwise wait for approval. Always call `embymedia_verify`. A queued or running task is not success.

For orphan findings, explain the Emby, STRM, and media facts. Never delete during a scan. Recovery: inspect the task and audit record, fix mount or mapping failures, then create a new plan; never replay an interrupted write blindly.
