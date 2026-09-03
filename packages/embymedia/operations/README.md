---
description: "Deterministic Emby, 115, STRM, metadata, user, and cleanup operations for deployments that need canonical previews, approval, execution, and independent verification."
kind: "package-reference"
---

# @embymedia/dsh-operations

English | [中文](README.zh.md)

## Summary

`dsh-operations` lets an EmbyMedia operator query live library facts and perform supported writes through a canonical plan, approval, execution, and verification flow. It distinguishes new library-root onboarding (`resource.add_new`) from missing-episode updates to an existing Series (`series.update`), and it supports explicit media and duplicate deletion through `media.delete` and `dedup.delete`. Write policy fails closed by default, while a staging mode limits plans to configured canonical library or 115 directory identifiers. Choose it only for a composition that supplies the required Host services, PostgreSQL state, upstream credentials, and EmbyMedia-specific deployment paths.

## Table of Contents

- [Use this package](#use-this-package)
- [Understand the implementation](#understand-the-implementation)
- [Further Exploration](#further-exploration)
- [Model Experience](#model-experience)
- [Known Limitations and Deferred Work](#known-limitations-and-deferred-work)
- [Dev Note](#dev-note)

-----

<a id="use-this-package"></a>
## Use this package

Mount the Host service in a composition that already supplies `webServer`, `agents`, `credentials`, and `typert`; mount the package's `/tools` entry inside the agent composition that should receive the EmbyMedia tools.

### When to choose it

Choose this package when writes must bind a deterministic preview to one session and principal, request approval according to risk, revalidate targets immediately before execution, and independently re-read facts before reporting completion. Avoid it for a generic Emby client or an installation without the package's PostgreSQL schema, credential service, 115 layout, STRM roots, and product Remote consumers.

### Minimal configuration

The Host row selects the environment variable that contains the PostgreSQL connection string. The selected variable must be non-empty when the service initializes; the surrounding composition supplies the injected services.

```yaml
- name: '@embymedia/dsh-operations'
  config:
    databaseUrlEnv: EMBYMEDIA_DATABASE_URL
```

| Field | Default | Meaning |
|---|---|---|
| `databaseUrlEnv` | `EMBYMEDIA_DATABASE_URL` | Environment variable containing the PostgreSQL connection string |
| `writeMode` | `disabled` | `disabled` rejects every plan; `staging` restricts targets to allowlists; `enabled` permits canonical targets |
| `stagingLibraryIds` | `[]` | Canonical Emby library IDs accepted in staging mode |
| `stagingCids` | `[]` | Canonical 115 directory IDs accepted in staging mode |
| `schedulerEnabled` | `false` | Whether the deployment starts scheduled work when writes are also enabled |
| `allowInsecureResourceApi` | `false` | Explicitly permit a credentialed non-loopback Resource API over HTTP; keep disabled unless the deployment has accepted plaintext transport |
| `approvalMode` | `manual` | `manual` requests approval for non-low-risk writes; `safe-auto` bypasses it only for the source-owned safe list |

[`src/config.ts`](src/config.ts) is the exhaustive source for paths, endpoints, concurrency, principal selection, environment overrides, validation, and every other accepted field. Deployment-specific values and credentials do not belong in this README.

### Read and write semantics

Read tools return bounded, structured facts for health, libraries, resources, Series state, analysis, tasks, audit, users, schedules, and configuration. `list_items` performs one server-side normalized title lookup that ignores punctuation, quote, symbol, spacing, width, and case differences, so the model does not probe name variants. Query-tool descriptions name each action's required identifiers and direct the model to the owning lookup. `embymedia_plan` derives every operation's accepted field list from the same specification used by validation. [`src/tools/index.ts`](src/tools/index.ts) owns the tool schemas and action roster.

A write starts with `embymedia_plan`. The Host validates exact inputs, captures explicit targets and verification facts, applies `writeMode`, stores the canonical confirmation and hash, and binds the plan to the calling session and configured principal. `embymedia_execute` accepts only the `planId`; the Host reloads the stored hash and confirmation, revalidates immediately before and after approval, identifies the preview hash and target IDs in the approval request, executes the supported handler, and runs independent verification before returning. Terminal verification reads still enforce Session and principal ownership. Cancellation or failure after an external effect produces durable partial or verifying state rather than a terminal cancellation that hides the effect.

`safe-auto` applies only to the package's bounded non-destructive set. `resource.add_new`, library scanning, poster application or repair, and metadata refresh may bypass a redundant approval after a deterministic plan. `series.update`, `media.delete`, `dedup.delete`, and every other high or critical operation still request approval.

### New resources and existing Series

Use `resource.add_new` to create a new media-library entry from either one verifiable 115 share root or 1–100 per-episode share candidates. Batch input uses `candidates:[{candidateId}]` plus a canonical scan object with `outputFolder` for the Series. The Host resolves the target CID, stages every extraction code, validates unique top-level names, transfers every share, writes all STRM files below that Series folder, waits for every media leaf, refreshes Emby once, and verifies all expected episodes. A batch performs one plan, one execution, and one scan.

Use `series.update` for missing episodes in an existing Series. One candidate may prove several gaps, or `candidates:[{candidateId}]` may combine 1–100 separate episode shares in one plan. The Host unions only supported video-leaf episode keys, rejects duplicate paths and uncovered requested episodes, resolves the existing Series CID, transfers every share there, waits for every requested leaf, scans the existing Series once, and verifies all requested gaps disappear while Series ID, path, and TMDB identity remain unchanged.

`gaps_summary`, `status`, and `workbench` compute at most 100 Series per page and report whether more Series remain. `gaps_summary` returns incomplete Series plus explicit blocked rows and omits paths and per-episode arrays, so an operator does not mistake TMDB/Emby authentication, rate-limit, cancellation, or upstream failures for zero gaps. `resource_plan` selects at most 20 gaps and returns the numbering mode, `gapTotal`, `deferredGapCount`, and `searchHasMore`; the operator must continue later Series and candidate pages rather than report whole-library or whole-Series completion from one response.

Resource search and `resource.stage_share` each produce an opaque, Session-bound `candidateId` for a 115 share. `resource.stage_share` accepts one user-provided title and share without returning its URL or extraction code. `accessCodeStaged=true` means the Host already holds that code for preview and execution; it is not an authentication blocker and the operator never asks the user to re-enter it. Public text is control-stripped, URL/credential-redacted, length-bounded, and array-capped. Results labeled `diskType=115` are exposed only when their URL parses as an official `115.com` or `115cdn.com` share. `resource.inspect_candidate` accepts the same-Session ID and returns at most 500 sanitized recursive evidence entries with compact episode-coverage counts, `evidenceTotal`, and `truncated`. `resource.list_entries` pages direct child facts; `resource.snapshot_share` pages shallow unprotected-share roots.

An ended Series with no per-gap candidate may use a single-root Series pack only as a replacement root, not as an in-place update. Before import, its snapshot must prove one transferable root and no conflicting TMDB marker; it need not enumerate every episode. `resource.add_new` creates a provisional separate root, and post-scan Emby facts must identify the new same-TMDB Series, prove `missingCount=0`, retain the old Series, and reject ambiguity. Only then may a separately approved `dedup.delete` retain the new complete Series and remove every old incomplete Series. Failed or incomplete onboarding leaves the old roots untouched.

`media.delete` removes explicit items and complete selected media roots. A Movie, Video, or Series may share one direct root only when the request selects every Emby media item below that root; the plan removes the root once. The executor asks Emby to remove each record while its source exists, then removes STRM and 115 storage. If Emby rejects item deletion, the executor still removes the exact approved storage roots, refreshes the Emby library, and waits for the now-absent source records to disappear before deciding success or partial. `dedup.delete` remains the keep-one operation for a complete current same-TMDB group. Critical cleanup requires one approval; a partial cleanup can retry only the same targets.

-----

<a id="understand-the-implementation"></a>
## Understand the implementation

<details>
<summary>Implementation internals — click to expand</summary>

The service keeps one durable operation record from preview through verification. Domain services produce deterministic previews; the planner stores their canonical projection; the executor checks ownership, expiry, policy, approval, and target freshness; the verifier records independently observed facts and the final terminal state. The service exposes Host Remotes for the browser UI, while the separate `/tools` entry adapts the same runtime to model-facing tools.

The Host also exposes `POST /internal/embymedia/tool` on its loopback listener for trusted same-host adapters. The route accepts the same closed wire-tool names and arguments as `/tools`, binds requests to one caller-supplied session ID, and returns the same structured result. Caddy rejects `/internal/*`, non-loopback peers fail closed, and the adapter supplies automatic one-shot approval while planning, target revalidation, audit, and verification remain authoritative.

| File | Role |
|---|---|
| [`src/service.ts`](src/service.ts) | Service lifecycle, Host Remotes, tool dispatch, health, and supported-operation projection |
| [`src/operations.ts`](src/operations.ts) | Canonical planning, risk metadata, exact-input validation, and write policy |
| [`src/execution.ts`](src/execution.ts) | Session-bound execution, approval, target revalidation, and audit transitions |
| [`src/verification.ts`](src/verification.ts) | Independent verification and terminal status recording |
| [`src/operation-runtime.ts`](src/operation-runtime.ts) | Current preview, handler, and verifier wiring for concrete operations |
| [`src/tools/index.ts`](src/tools/index.ts) | Agent tool registrations and structured result rendering |
| [`src/invariant.ts`](src/invariant.ts) | Stable-identifier uniqueness checks for package-owned rosters |

</details>

-----

<a id="further-exploration"></a>
## Further Exploration

Read these pages when the package-level behavior is not enough. They move from Cordis composition to the packaged operator profile and browser presentation.

- [Cordis primer](../../../docs/cordis-primer.md) — plugin rows, injection, and composition behavior.
- [EmbyMedia preset](../preset/README.md) — the operator persona and eight operation skills that consume these tools.
- [EmbyMedia client UI](../ui/README.md) — tool cards, credential staging, and the Remote-backed workspace.
- [`src/capabilities.ts`](src/capabilities.ts) — stable package vocabulary and operation identifiers.

-----

<a id="model-experience"></a>
## Model Experience

### EmbyMedia tools and structured results

#### What the model sees

When the `/tools` entry is mounted, the model receives the package's bounded `embymedia_*` query tools plus `embymedia_plan`, `embymedia_execute`, and `embymedia_verify`. Tool results are JSON projections of current facts, canonical plans, execution state, and verification state; the package does not add a system-prompt section.

#### Token effect

The fixed tool schemas consume request tokens while the entry is active. Each call adds a data-dependent JSON tool result; result size follows the selected query limit or the bounded explicit plan targets rather than an unbounded catalog dump.

#### KV Cache effect

The tool-schema prefix is stable for the life of a preset composition. Tool calls and results append after that prefix, so they affect the conversation suffix without rewriting earlier request content; changing the mounted preset establishes a different prefix for the new session.

## Known Limitations and Deferred Work

<a id="known-limitations-and-deferred-work"></a>

These constraints define when the package is unavailable or requires a new plan.

- **Deployment-specific infrastructure** — startup requires a PostgreSQL connection and the Host service dependencies; individual operations also fail when their Emby, 115, TMDB, resource API, filesystem, or credential prerequisites are absent.
- **Only complete operation triples are advertised** — a kind is reported as supported only when preview, execution, and verification are all wired; declared but incomplete kinds cannot be planned through the runtime.
- **Share access codes are process-local** — resource search and `resource.stage_share` expose only a session-bound candidate ID; candidate or plan expiry and service restart discard the value, so an unexecuted protected-share update must stage a fresh candidate. Persisted confirmations and final verification never recover or expose the code.
- **Add-new accepts a narrow share form** — `resource.add_new` requires one wrapped, non-empty 115 share root and supports only the `keep` old-version policy; magnet/offline onboarding needs a separate workflow.
- **Writes and scheduling are opt-in** — `writeMode` defaults to `disabled`, and the scheduler remains disabled unless both deployment settings permit work.

<a id="dev-note"></a>
### Dev Note

<details>
<summary>Working context for maintainers — click to expand</summary>

None.

</details>
