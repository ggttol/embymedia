# Agent Note: Keep only the standalone V2 source tree

Status: implemented

English | [中文](2026-09-10-standalone-v2-source-tree.zh.md)

## Problem

The repository carries a general-purpose DeepSeek Harness/Cordis runtime beside the independent Go media application. Its plugin graph, language SDKs, application builds, generated manuals, and model-dependent CI impose installation and maintenance costs without serving the supported media runtime. Two sets of instructions also make it easy to build the wrong application or infer that media operations require model credentials.

## Decision

The supported source is `cmd/server`, `internal`, the independent `web` pnpm project, and current deployment, backup, restore, login, and media-maintenance tooling. `make install-web` performs the frozen frontend install; `make build` builds Vue and the Go executable. `make test` covers Go race tests and deployment Python tests; `make check` covers Vue typechecking, Go vet, and documentation. `make build-linux` produces the Linux binary and checksum. CI exercises these V2 paths without a DeepSeek key.

General-purpose Harness/Cordis applications, SDKs, vendored runtime, build machinery, CI, generated catalogs, and manuals are outside the supported product. Historical implemented notes move into the frozen archive rather than remain standing instructions. The obsolete workspace-write approval record is historical; V2's token scopes, browser-owned dangerous-action switch, target-bound deletion approval, and native Emby administrator checks remain current authority.

The obsolete proposal groups for Harness event schemas, cancellation seams, session projections, dynamic plugins, composer behavior, compaction, task surfaces, SDK publication, package inventories, and Harness-only testing are rejected because their production owner is removed. Their records and existing Harness-only rejections are deleted because they cannot prevent a plausible standalone-media mistake. General documentation and deterministic-testing goals remain in the concise current guides, not as an unimplemented Harness project.

The source cut does not alter `/opt/embymedia-v2/current`, the production SQLite format, Emby/CloudDrive integration, browser identities, STRM ACLs, or backup/restore formats. It does not authorize deployment, credential rotation, database or media deletion, or cleanup of ignored local files. Existing copyright and applicable dependency attribution remain required. Current media identity, recovery, normalization, and access decisions remain active; existing archive artifacts and seals remain immutable.

## Alternatives considered

**Retain the unused runtime as a root workspace.** The original plugin architecture supports interchangeable model providers, durable conversation replay, extensible tools, and client SDKs. Those are meaningful capabilities for an agent platform, but they add another product's toolchain and maintenance obligations to a media service that does not call them.

**Leave all manuals and decisions active but mark the runtime optional.** This retains conflicting build, security, and credential instructions. Frozen historical notes preserve rationale without presenting deleted capabilities as supported.

**Rewrite or delete the historical archive.** That would discard sealed decision evidence and break the archive's integrity guarantee. New archives append seals; old records remain byte-for-byte historical snapshots.

**Replace the production database or redeploy during source cleanup.** Neither is necessary to remove unused source. Preserving production state and requiring a separately authorized release avoids coupling repository maintenance to irreversible media effects.

## Consequences

The checkout gives up developing and distributing a general-purpose agent platform, its SDKs, and its model replay suites. Reintroducing those capabilities requires an explicit product decision, a demonstrated V2 consumer, independent dependency ownership, and corresponding security and recovery verification; a historical note alone does not justify restoring the monorepo.

The required verification is a clean-source frozen web install, V2 build, race/deployment tests, typecheck/vet/docs checks, and Linux artifact construction. Existing media tests must still cover source identity, destructive authorization, task interruption, and backup/restore behavior. These requirements do not claim that a live deployment or destructive acceptance operation was performed. Frozen notes retain their original historical evidence, including tests and paths belonging to removed code.
