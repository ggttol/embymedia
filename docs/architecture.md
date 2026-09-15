# EmbyMedia architecture

English | [中文](architecture.zh.md)

## Summary

EmbyMedia runs media workflows in one Go process and exposes the same service operations to its browser and Agent clients. SQLite persists configuration and work; Emby, CloudDrive2, 115, and the resource index own their external state.

## Table of Contents

- [Runtime and ownership](#runtime-and-ownership)
- [Persistent work](#persistent-work)
- [Security and media identity](#security-and-media-identity)

## Runtime and ownership

[`cmd/server`](../cmd/server) assembles REST, embedded Vue assets, OpenAPI, Streamable HTTP MCP, legacy SSE, and optional stdio. [`internal/api`](../internal/api) and [`internal/mcp`](../internal/mcp) call shared [`internal/service`](../internal/service) implementations; neither transport defines a second media executor. [`web`](../web) is an independent pnpm project whose build becomes embedded assets.

[`internal/storage`](../internal/storage) owns monotonic SQLite migrations, settings, accounts, token digests, task attempts, schedules, and audits. The process locks its database before migration and queue recovery. A second HTTP, stdio, or maintenance process cannot share ownership. Use HTTP MCP to share the running instance.

Audit writes normalize timestamps to UTC while preserving nanoseconds. Time filters compare instants and include both endpoints, independent of the server's local timezone.

The deployment runs the application on loopback behind Caddy. The Python HTTP login service owns browser users and signed sessions; Compose owns Emby and CloudDrive2. No Node application server, Cordis plugin loader, SDK runtime, or PostgreSQL instance participates in V2. [Operations](operations.md) owns installed paths and recovery procedures.

## Persistent work

The queue persists pending work and attempt outcomes; schedules create linked executions. Interrupted effectful work normally fails for explicit review rather than replaying provider mutations. Quark-to-115 imports are the narrow exception: durable source identities, spool offsets, multipart parts, and verified destination identities let startup reconcile and resume without resaving the share or duplicating bytes. Ambiguous provider state still fails for review.

An optional NAS transfer worker moves only the Quark-to-115 data plane out of the database-owning server. Debian keeps provider selection, source and destination identities, task state, cancellation, and final 115 verification; a forced-command SSH key sends one bounded file request over encrypted stdin to a stateless worker invocation. The NAS keeps its own durable segment checkpoints and spool, downloads Quark ranges without environment proxies, publishes through an independent CloudDrive2 mount, and removes the spool only after Debian verifies the exact 115 object. Neither the worker nor the NAS CloudDrive2 instance opens or copies the production SQLite database.

A verified manual Quark import deterministically queues one `media_ingest` continuation. The continuation scans existing Emby television libraries and accepts exactly one Series only when every numbered video title agrees with its canonical or original title and at least one file also proves the canonical year or TMDB ID. It revalidates every 115 object by persisted ID, parent, name, size, and SHA-1, moves videos and related subtitles into the existing canonical Series directory, assigns collision-safe episode names, synchronizes that library's STRM output, runs a tracked Emby scan, and verifies every imported aired episode. Ambiguous identity, unsupported files, and review findings remain in `_待整理`; the workflow reports them without synthetic success.

Full synchronization performs one ordered CloudDrive FUSE source walk, reconciles STRM output, verifies every target, requests an Emby scan, and waits for provider completion. Its source progress is estimated from the prior local STRM inventory and reported every 500 media files. The hourly schedule uses `sync_strm=false` to run only the tracked Emby scan; one daily full synchronization remains the eventual-consistency fallback because production CloudDrive webhooks may be disabled. Internal media mutations continue to run their existing scoped STRM synchronization before Emby verification. STRM reconciliation preserves original media paths, rejects ambiguous canonical destinations, requires a readable mount canary before stale output removal, and retains shared Emby access through filesystem ACLs.

Automatic completion searches 115 and Quark independently and determines candidate usability from live share trees without filtering by resource-index health labels. Before the missing-episode scan, every TMDB-bound Series in an eligible library receives a best-effort Emby metadata refresh request, so episodes published to TMDB after Emby's cached season snapshot become visible; Emby's own provider throttling may still delay a cycle and refresh failures are logged without stopping the run. Preview mode performs no provider writes and reports provider-qualified evidence; multiple usable resources for one episode remain an explicit conflict until the operator selects a resource. For ordinary episodes, a live path must name the canonical or original Series title; candidate and share metadata can corroborate only the canonical year or TMDB ID, so noisy metadata titles cannot override the live path. Selected 115 leaves retain the direct exact-directory path. Selected Quark leaves run as durable child imports: the server validates and persists the configured 115 library, canonical Emby Series, exact target directory, source IDs, and expected episodes before saving the share, then resumes download and upload checkpoints and completes only after 115, STRM, and Emby verification. Public task requests cannot supply the internal binding. Unavailable shares, unmatched episodes, and conflicts complete as visible findings instead of failing the whole scheduled run or cancelling valid work for another Series. Cancellation, rate limiting, account authorization, transport failure, provider 5xx responses, configuration faults, and post-write verification failures remain real task errors; the hourly schedule supplies bounded later retry instead of immediate provider hammering. The [provider-qualified completion decision](../.agents/notes/implemented/feature/2026-09-13-provider-qualified-episode-completion.md) owns these boundaries.

## Security and media identity

REST and MCP share Agent authentication, scopes, rate limits, and redacted audits. Caddy strips caller-supplied browser identity from public Agent paths; explicit invalid credentials cannot fall back to browser trust. The browser-owned deletion switch and target-bound approval remain separate from Agent read/write scope.

Missing-episode completion requires the intended TMDB-bound Emby Series to own the requested episodes, not merely visible 115 files or STRM paths. Native Emby deletion requires an authorized Emby administrator device session and revalidated original-source identity. Partial outcomes remain visible and are not silently replayed. The [Series decision](../.agents/notes/implemented/bug-fix/2026-08-31-canonical-series-missing-episode-binding.md) owns the rationale.
