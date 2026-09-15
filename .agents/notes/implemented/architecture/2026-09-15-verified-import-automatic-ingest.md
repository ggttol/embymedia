# Agent Note: Verified imports continue into automatic catalog ingest

Status: implemented

English | [中文](2026-09-15-verified-import-automatic-ingest.zh.md)

## Problem

A manual Quark import ended after every file reached the fixed `/emby/_待整理` directory and passed 115 identity verification. Moving, canonical naming, STRM generation, and Emby ingestion remained separate maintenance work, so the task UI could show a green completed transfer while the media was still unavailable in a library.

The global normalization planner cannot safely serve as an automatic per-import continuation. It inventories the complete 115, STRM, and Emby catalogs and intentionally leaves provider files without an existing Emby identity as findings; applying one whole-catalog plan can also include unrelated operations.

## Decision

Every verified, manually submitted `quark_to_115_import` deterministically queues one `media_ingest` continuation. The UUID derives from the source task ID, so reconciliation and transfer retries cannot duplicate the continuation. Autofill-bound imports keep their existing inline STRM and Emby verification and do not enqueue a second ingest.

The continuation accepts only existing Emby television identities. Every numbered video filename must agree with the canonical or original Series title, and at least one imported filename must also prove the canonical year or TMDB ID. Exactly one matching Series and one same-name 115 Series directory must exist. The task revalidates each source object by persisted 115 ID, parent, name, byte size, and SHA-1 before moving it.

Videos and related subtitles move into the existing canonical Series directory and receive an explicit `SnnEnn` name while preserving the release suffix. Occupied names retain alternate bytes under a SHA-1 suffix; exact existing bytes stay in staging instead of being overwritten or deleted. Unsupported files, unnumbered video, ambiguous Series matches, and other findings remain in `_待整理`.

After organization, the task synchronizes only the matched library's STRM output, runs a tracked Emby scan, and verifies every imported aired episode number against the original Series ID. The task fails unless that post-scan proof succeeds. Task Center groups the transfer and continuation as one seven-stage workflow and directs cancel or retry to the active child stage.

## Alternatives considered

**Automatically apply the complete normalization plan after every import.** Rejected because the plan owns the whole catalog, may include unrelated reviewed operations, and cannot infer identities for newly staged files that Emby has not ingested.

**Move every import into a folder named from the share title.** Rejected because release titles are not media identities and can silently create duplicate or wrong Series roots.

**Run only STRM synchronization and Emby refresh.** Rejected because `_待整理` is excluded from the catalog; scanning cannot substitute for a verified move into a canonical library.

**Report transfer and ingest as unrelated execution cards.** Rejected because an operator needs one visible end state and must not mistake file transfer for library availability.

## Consequences

Manual Quark imports become end-to-end only when identity evidence is unique and the final Emby episode proof succeeds. Ambiguous or unsupported content still completes the transfer task but the grouped workflow ends with a visible review finding; no provider mutation occurs for rejected files.

The queue now contains an internal continuation task type, persistent parent reference, per-file move evidence, and post-scan result. Catalog ingest reuses existing media mutation serialization, provider identity checks, STRM containment, and Emby completion tracking rather than introducing another scheduler or database owner.
