---
name: emby-resource-onboarding
description: Use for 115 cookie checks, share parsing or snapshots, resource search, duplicate context, transfers, offline tasks, and one-stop add-new workflows.
---

# Resource onboarding

Use `embymedia_resource` to search and inspect only same-session opaque candidates whose `diskType` is `115`. `accessCodeStaged=true` is normal: the Host already retained the share extraction code behind `candidateId`; never ask the user to provide it, never use a credential card for it, and never describe it as a blocker. `inspect_candidate` returns sanitized roots and bounded recursive evidence without the share URL or access code; reject only an expired, invalid, multi-root, mislabeled, or incomplete candidate.

When a new Series is available only as separate per-episode 115 candidates, identify one unique candidate for every intended episode and create one `resource.add_new` plan with `candidates:[{candidateId}]` plus a canonical scan object whose `outputFolder` is the Series folder. Do not call `inspect_candidate` separately for every episode: batch plan validation snapshots all candidates and binds every leaf before writing. The Host resolves the target CID from the scan library, transfers all shares, writes every STRM below `outputFolder`, waits for all leaves, and scans once. Never run one write per episode or continue searching for a nonexistent pack after the complete episode set is identified.

Transfers and add-new are high-risk writes. Plan `resource.add_new`; in `safe-auto` mode execute this allowlisted non-destructive plan immediately without a redundant approval question, otherwise wait for approval. Destructive or replacement cleanup is never auto-approved.

Completion requires: 115 acceptance, resource visibility, STRM generation, Emby scan, metadata/poster check, duplicate handling, old-version policy, and final verification. “115 accepted” alone is pending, never success. An ended-Series replacement candidate needs one transferable Series root and no conflicting TMDB marker; its share snapshot need not prove every episode. The provisional new root uses `{candidate:{candidateId,targetCid},scan:{libraryId,libraryName,mediaFolder}}`, and the add-new pipeline already performs its scan. Only post-scan Emby facts can prove completeness: a read-only same-TMDB check must identify the new Series with `missingCount=0` before a separate approved `dedup.delete` removes every old incomplete root. On partial failure, report the last verified stage and create no implicit retry, separate scan, or cleanup.
