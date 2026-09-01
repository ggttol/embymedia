---
name: embymedia-backup-restore
description: Use for backup health, snapshot interpretation, retention, off-host copy status, and planning an isolated restore or rollback rehearsal.
---

# Backup and restore

Read backup health through `embymedia_health` and diagnostics. State the last successful local snapshot, 115 copy status, covered components, and age; never claim protection from a repository that was not listed and decrypted.

A restore is isolated first. Validate PostgreSQL, DSH session/credentials, Emby config, CloudDrive config, Authelia, and STRM artifacts without writing production paths. Do not traverse or back up media payloads.

Before a production recovery, present the chosen snapshot, expected RPO, stopped services, destination, and verification gates. Failure recovery restarts the healthy dependency order; never leave an empty mount visible to Emby or DSH.
