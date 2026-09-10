# Agent Note: Canonical Series binding owns missing-episode completion

Status: implemented

English | [中文](2026-08-31-canonical-series-missing-episode-binding.zh.md)

## Problem

Visible 115 files or STRM paths prove storage discovery, not that Emby assigned the requested missing episodes to the intended Series. Similar titles, duplicate TMDB identities, and replacement roots can leave the original Series incomplete. A protected share's transient access code cannot be the authority for durable completion.

## Decision

`series_auto_fill` is restricted to the exact `电视剧追更` and `综艺追更` libraries and their same-name 115 CID mappings. It requires one TMDB-bound Series and one exact direct-child 115 folder, inventories aired numbered gaps, searches a bounded 1–30 valid candidates per Series, and recursively inspects at most 10,000 entries through depth 20. Only supported video leaves with explicit matching episode notation can prove coverage; parent titles and pack ranges cannot invent missing leaves. Candidate labels and each video's ancestry must independently agree with the Series identity.

Ordinary transfer receives exact selected leaf IDs into the existing Series CID, waits for CloudDrive visibility, synchronizes STRM, scans Emby, and verifies that the original Series owns the requested episodes. Inspection errors remain task failures with per-Series results, distinct from a successfully inspected share with no suitable episodes. Final Emby identity and gap verification do not depend on reopening a protected share or retaining its access code.

With `replace_completed_pack` and the browser-owned dangerous-action switch enabled, the task may stage one distinct root only when its video leaves cover every expected aired episode. It verifies the complete replacement Series before deleting the old Emby item, recycling the old 115 root, removing its contained STRM root, and scanning again. Failed attempts remove only verified operation-owned staging; an uncertain cutover requires recovery rather than deletion of either unverified copy.

The Drive service serializes share snapshot requests through response closure. `share_snapshot_interval_ms` defaults to 1000 milliseconds between the prior response and the next request; zero removes delay but not serialization. Cancellation stops waiting. Typed HTTP 405/429 errors halt subsequent candidates and Series while preserving partial evidence; status-like text in a successful response is not an HTTP failure. Incomplete snapshots cannot authorize transfer, and pacing does not guarantee provider acceptance.

Native Emby deletion requires an enabled administrator's authorized device session and each item's `CanDelete` permission; browser proxy identity, Agent tokens, and application API keys cannot substitute. Confirmation includes generated STRM and original-video paths. The gateway serializes media mutations, checks exact native `DeleteInfo` paths, and recycles identity-revalidated 115 originals only after native deletion succeeds. Recycling failure remains an audited partial outcome without rollback or automatic replay. Default filesystem ACLs preserve shared Emby write access to generated directories without weakening the service umask.

## Testing

Standalone queue tests cover fixture-compatible Emby gap discovery, resource search, recursive 115 inspection, selective receive, CloudDrive visibility, STRM generation, scan completion, and post-scan verification. Replacement tests require complete coverage and the dangerous-action switch and enforce verification before old-root deletion. Other cases reject ineligible libraries, duplicate TMDB Series, future or unnumbered episodes, and resolution-like digits mistaken for episode keys.

Standalone deletion tests cover administrator/device authorization, application-key rejection, original-path confirmation, source identity drift, native failures, and partial recycling. Historical live acceptance used an isolated two-episode fixture to establish sibling preservation during episode deletion and original recycling during Series deletion; it does not authorize repeating destructive tests on production media.

## Alternatives considered

**Treat visibility as completion.** A second root or incorrectly bound episode can be visible while the intended Series retains its gaps. Current Series identity and episode ownership define success.

**Resolve destinations or delete duplicates by name alone.** Names can drift or collide. Exact Emby identity, storage IDs, path relationships, and uniqueness checks prevent ambiguous targeting.

**Delete the old root before verifying the replacement.** A transferred pack can be incomplete or parsed into the wrong Series. Verification of every expected aired episode must precede removal of the old copy.

**Reopen the protected share for final verification.** Share availability and an access code are not the postcondition. Durable Emby, TMDB, episode, and placement facts remain observable without credential recovery.

## Consequences

Completion means that the intended Series owns its requested episodes, or that a verified complete replacement becomes the sole same-TMDB keeper after old-root deletion. Ambiguous identity, incomplete resource evidence, and uncertain external effects fail closed. Explicit automatic replacement adds unattended deletion risk, bounded by the task option, browser switch, complete coverage, and ordered verification. Ordinary file transfer does not promise missing-episode completion.
