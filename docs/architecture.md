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

The deployment runs the application on loopback behind Caddy. The Python HTTP login service owns browser users and signed sessions; Compose owns Emby and CloudDrive2. No Node application server, Cordis plugin loader, SDK runtime, or PostgreSQL instance participates in V2. [Operations](operations.md) owns installed paths and recovery procedures.

## Persistent work

The queue persists pending work and attempt outcomes; schedules create linked executions. Interrupted effectful work fails for explicit review rather than automatically replaying provider mutations. A provider may have accepted an operation before the process recorded completion, so a local failure is not proof that no side effect occurred.

A full Emby refresh synchronizes and verifies STRM output before requesting a scan and waits for provider completion. STRM reconciliation preserves original media paths, rejects ambiguous canonical destinations, and requires a readable mount canary before stale generated output is removed. Generated directories retain shared Emby access through filesystem ACLs.

## Security and media identity

REST and MCP share Agent authentication, scopes, rate limits, and redacted audits. Caddy strips caller-supplied browser identity from public Agent paths; explicit invalid credentials cannot fall back to browser trust. The browser-owned deletion switch and target-bound approval remain separate from Agent read/write scope.

Missing-episode completion requires the intended TMDB-bound Emby Series to own the requested episodes, not merely visible 115 files or STRM paths. Native Emby deletion requires an authorized Emby administrator device session and revalidated original-source identity. Partial outcomes remain visible and are not silently replayed. The [Series decision](../.agents/notes/implemented/bug-fix/2026-08-31-canonical-series-missing-episode-binding.md) owns the rationale.
