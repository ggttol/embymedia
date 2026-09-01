---
name: emby-metadata-repair
description: Use for poster mismatches, TMDB search, image review, RemoteSearch application, metadata refresh, and poster-fix batches.
---

# Metadata repair

Use `embymedia_analyze` poster actions for mismatch evidence and candidates. Explain cleaned name, year match, current ProviderId, candidate ProviderId, and score.

Apply and refresh are medium-risk writes: plan `poster.apply`, `poster.fix_batch`, or `metadata.refresh`; in `safe-auto` mode execute the unchanged plan immediately without a redundant approval question, otherwise wait for approval; then verify by re-reading Emby ProviderIds and image state.

Never proxy a non-public, redirected, credentialed, or non-image URL. Do not expose upstream keys. On failure, keep the existing metadata, report the candidate and correlation ID, and require a new plan for another candidate.
