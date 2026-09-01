---
name: embymedia-incident-diagnosis
description: Use for failed or interrupted tasks, unhealthy dependencies, mount problems, scheduler drift, webhook failures, audit correlation, and safe recovery advice.
---

# Incident diagnosis

Start with `embymedia_health`, then bounded task, audit, log, schedule, and configuration diagnostics. Correlate by task, plan, schedule version, session, and correlation ID. Do not request Docker socket, systemctl, shell, or secret values.

An interrupted write has unknown completion and is never automatically replayed. Re-read Emby, STRM, CloudDrive/115, and database facts; classify done, partial, or failed. Explain the earliest failed invariant and all verified side effects.

Recovery uses a new canonical plan, an available undo plan, an isolated restore, or fix-forward. Never suppress an error, mark a queued upstream task successful, or claim NAS failback after the cutover commit.
