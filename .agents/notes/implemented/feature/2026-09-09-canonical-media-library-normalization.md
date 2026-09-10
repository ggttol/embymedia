# Agent Note: Canonical media library normalization

Status: implemented

English | [中文](2026-09-09-canonical-media-library-normalization.zh.md)

## Problem

A managed 115 root can retain provider-valid files while its folder names, STRM paths, and Emby libraries describe different classifications. Path-only cleanup cannot safely merge season folders, distinguish exact duplicates from alternate versions, preserve Blu-ray structures, or migrate user playback state when Emby item IDs change. Large movie files also need a separately visible playback tier, while active television and variety tracking must remain the first libraries users see.

## Decision

The normalization workflow freezes complete 115, STRM, Emby, and non-default user-state inventories before mutation. A SQLite plan binds every operation to the 115 object ID and its expected parent ID, name, byte size, and SHA1. The executor creates destination directories before file operations, accepts only the expected or already-planned target name during resume, verifies each completed target group, and stops on HTTP 405 or an expired Cookie. Completed rows are never replayed.

The provider root remains `emby`. Immediate 115 directory names, STRM directory names, and Emby library names use the same catalog: `电视剧追更`, `综艺追更`, `最新电影`, `电影`, `IMAX巨幕`, `电视剧`, `动漫`, `动漫电影`, `儿童`, `综艺`, and `纪录片`. The two tracking libraries lead every user's Emby home order. The automatic `合集` library remains separate from source libraries.

The planner uses TMDB identity, media type, measured file size or bitrate, verified genres, and current tracking status in that order. `IMAX巨幕` accepts Movie files of at least 45 GB or 45 Mbps; its name is a playback-tier label and does not assert an official IMAX release. Existing child curation outranks general animation classification after the high-bandwidth rule. Unmatched identities, invalid episode numbers, conflicting episode assignments, unsupported disc structures, and unbound sidecars remain review findings.

Movie directories use `{title} ({year}) [tmdbid={id}]`. Series directories add `Season NN`, while filenames retain an explicit `SnnEnn` or multi-episode range. Filename truncation reserves the episode, version, and SHA1-disambiguation suffix. External subtitles with an exact source-stem relationship move with their video. Full STRM synchronization skips configured immediate review roots and recognizes legacy AVI, FLV, ISO, RealMedia, VOB, and WMV containers.

Exact duplicate video bytes are eligible for consolidation only when non-empty SHA1, byte size, and logical Movie or Series/season/episode identity agree. One canonical object remains; other copies move under `_待回收`. No normalization script performs permanent deletion. Residual source folders and ambiguous media move under `_待整理`, while every configured reserved root remains at the provider root.

Emby cutover creates parallel libraries on `/strm-v2`, uploads one coordinated 16:9 archive poster per media library and the collections library, scans the complete replacement catalog, and verifies every expected provider identity, planned alternate version, and episode number, including every number represented by a multi-episode file. Cutover re-verifies replacement IDs, paths, types, and catalog contents immediately before deleting only the recorded old library IDs. User state maps through TMDB plus season and episode; duplicate old state rows merge with logical OR for played/favorite values and maximum resume position/play count, every alternate destination version receives the merged logical state, and a played Series marks each replacement episode played because Emby derives Series state from its children.

## Alternatives considered

**Rename paths directly from release names.** Rejected because release strings do not prove media identity, season membership, or duplicate equivalence, and an interrupted path-only batch cannot distinguish completed work from changed input.

**Copy the complete media root before cutover.** Rejected because 115 server-side moves preserve object identity without duplicating the media corpus. Frozen preconditions, staged review roots, parallel STRM output, and delayed Emby deletion provide recovery without a second 178 TB media tree.

**Treat every large or `IMAX`-named file as official IMAX media.** Rejected because file size and playback bitrate describe device and network cost, not a licensed presentation format. The library artwork states `高码率 · 大文件`.

**Permanently delete SHA1 duplicates during migration.** Rejected because incorrect metadata can assign identical bytes to different logical episodes. The planner blocks conflicting identities and stages only identity-equivalent duplicates for a separately authorized recycle operation.

## Consequences

Normalization owns additional SQLite inventory and plan artifacts, a second STRM volume during cutover, reviewed identity overrides, and a user-state migration step. Provider throttling may require a refreshed Cookie, but every accepted operation remains recorded at a verified group boundary. The resulting catalog trades release-oriented source groupings for stable media identities, clear playback tiers, first-class tracking libraries, reversible duplicate staging, and matching 115, STRM, and Emby names.
