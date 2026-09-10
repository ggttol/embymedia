# Agent Note: Live share paths own episode title identity

Status: implemented

English | [中文](2026-09-10-live-share-path-identity.zh.md)

## Problem

Resource-index and share titles are discovery metadata. A readable direct episode can use the exact Series title in its filename while omitting the release year and TMDB ID, and its indexed title can contain an incorrect extra prefix. Requiring metadata and the live file path to identify the Series independently rejects that valid episode; accepting loose title matches in the live path would instead admit similarly named Series.

## Decision

This decision partially supersedes the ordinary single-episode identity rule in [Canonical Series binding owns missing-episode completion](2026-08-31-canonical-series-missing-episode-binding.md). Completed-pack replacement keeps that decision's stricter root and leaf identity requirements.

Ordinary episode completion distinguishes inspected 115 path labels from resource metadata. Every non-generic directory or video title in the inspected path must equal the canonical Emby title or `OriginalTitle`, unless that label carries the exact canonical TMDB ID. The inspected path must establish an exact title or TMDB match. Conflicting TMDB IDs, release years, or seasons reject the candidate.

Candidate and share titles cannot satisfy the inspected-path requirement, and their unrelated title text cannot authorize or veto transfer. They may corroborate an exact live-path title only with the canonical TMDB ID or the canonical release year. A direct filename without a year therefore remains eligible when a candidate or share title supplies the same release year, while a candidate with a conflicting year or TMDB ID still fails closed.

Explicit requested episode notation, the unique existing 115 Series folder, bounded inspection, exact leaf transfer, CloudDrive visibility, STRM synchronization, and post-scan ownership by the original Emby Series remain required.

## Testing

A standalone regression covers two direct `交锋` episode files without embedded years: one exact indexed title and one indexed title with an incorrect prefix, both corroborated by release year. Existing cases continue to reject an untitled direct episode, an unrelated live directory or filename, conflicting TMDB IDs, years, and seasons, and a title-only match when the canonical Series has no release year.

## Alternatives considered

**Require every candidate title to equal the Series title.** Search titles are not observed storage paths and can contain editorial prefixes or mistakes that do not appear in the readable share.

**Accept an exact live filename without corroboration.** A remake or another Series with the same title could expose the same episode notation. A matching canonical year or TMDB ID remains necessary.

**Allow substring matches in inspected paths.** Prefix and suffix collisions such as another title ending in the canonical title would authorize the wrong media.

## Consequences

Noisy index titles no longer veto exact inspected files, and yearless direct episode filenames can complete a gap when independent metadata supplies the canonical release year. Shares with no matching year or TMDB evidence remain unresolved even when their filename matches. Final Emby verification still determines completion after transfer.
