---
description: "Packaged Emby operator preset assets and entry-point constants for compositions that load a focused persona, EmbyMedia tools, and eight operation skills through dsh-agent-presets."
kind: "package-library"
---

# @embymedia/dsh-preset

English | [中文](README.zh.md)

## Summary

`dsh-preset` gives an agent-preset consumer the packaged `emby-operator` composition and the filesystem root that contains it. A session using that preset receives a Chinese EmbyMedia operator persona, the EmbyMedia tool entry, skill discovery, the skill tool, the ask-user tool, and eight focused operation skills. The composition deliberately excludes shell, filesystem, web, MCP, dynamic Cordis, subagent, and workflow capabilities. This package is a plain asset-and-constant library: it has no Cordis plugin entry, profile patch, or package bin.

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

Consume the exported root and preset ID from the Host composition that configures `@deepseek-ai/dsh-agent-presets`; do not mount this package as a Cordis row or treat it as an installable profile layer.

### When to use it

Use this package when the deployment already mounts the EmbyMedia Host service and needs a narrowly scoped operator session with product tools, approval-aware instructions, and domain skills. Use a different preset when the agent needs general shell, filesystem, web, delegation, or workflow capabilities, or when the persona and trust policy must be deployment-configurable.

### Entry point

The entry exports the packaged preset directory and its stable preset ID:

```text
import { EMBY_OPERATOR_PRESET, PRESET_ROOT } from '@embymedia/dsh-preset'

const agentPresetConfig = {
  default: EMBY_OPERATOR_PRESET,
  includeShippedRoot: false,
  includeUserRoot: false,
  roots: [{ path: PRESET_ROOT, trust: 'system' }],
}
```

Pass that configuration to the composition's existing `@deepseek-ai/dsh-agent-presets` row. Success means its roster can discover `emby-operator` from `PRESET_ROOT`; a missing asset, unloadable row, or unavailable dependency is reported by the preset consumer rather than by this library. No package-bin or `dsh plugin` command belongs to this package.

### What the preset composes

The preset mounts five rows in one session-scoped composition: the complete operator persona, `@embymedia/dsh-operations/tools`, filesystem-backed skill discovery, the `skill` tool, and `ask_user_question`. The Host-level `@embymedia/dsh-operations` service remains outside this package and must already be available to the tool row.

The eight bundled skills cover library operations, new-resource onboarding, existing-Series follow-up, metadata repair, cleanup and deduplication, user policy and settings, backup and restore, and incident diagnosis. Skill descriptions determine when the model loads each body; the README does not duplicate their complete procedures.

### Operator policy

The persona treats each latest user request as an independent task, requires deterministic read-first facts, and routes writes through one canonical plan followed by Host-owned execution, approval, and verification. Ordinary deletion uses `list_items` then one `media.delete` plan; it never exposes dedup, scan, retry, storage-root, or recovery choices unless the requested operation itself requires them. Repeated blockers and terminal operation states end the task instead of starting exploratory tool loops. In `safe-auto` mode, only the Host's bounded safe list proceeds without approval; destructive and replacement operations still require one approval.

Missing episodes in an existing Series normally use `embymedia_series` gap and resource-plan queries followed by `series.update` with canonical library and Series IDs and explicit requested episodes. When an ended Series has no per-gap candidate, the operator may import a complete pack as a provisional new root, prove the new same-TMDB Series has no gaps, and only then run a separately approved `dedup.delete` that retains the new Series and removes every old incomplete root. Final verification uses canonical Host facts and never depends on a 115 access code.

-----

<a id="understand-the-implementation"></a>
## Understand the implementation

<details>
<summary>Implementation internals — click to expand</summary>

The runtime entry computes `PRESET_ROOT` relative to the built module and exports `EMBY_OPERATOR_PRESET` as the directory ID. The agent-preset service owns discovery and mounting; this package owns only the asset tree and its internal composition. Each skill remains an independent filesystem unit so the model can load only the instructions relevant to the current task.

| File | Role |
|---|---|
| [`src/index.ts`](src/index.ts) | Packaged preset root and stable preset ID exports |
| [`presets/emby-operator/preset.yml`](presets/emby-operator/preset.yml) | Roster display metadata |
| [`presets/emby-operator/agent.cordis.yml`](presets/emby-operator/agent.cordis.yml) | Persona and session-scoped plugin composition |
| [`presets/emby-operator/skills/`](presets/emby-operator/skills/) | Eight operation skill packages loaded through the skill tool |

</details>

-----

<a id="further-exploration"></a>
## Further Exploration

Read these pages when the packaged composition is not enough. They move from preset mounting to the Host operation behavior and browser presentation.

- [Agent presets](../../preset/agent-presets/README.md) — roster roots, trust, session composition, and failure handling.
- [EmbyMedia operations](../operations/README.md) — canonical planning, approvals, execution, and verification.
- [EmbyMedia client UI](../ui/README.md) — the cards and Remote-backed workspace paired with the operator.
- [Cordis primer](../../../docs/cordis-primer.md) — composition rows and injection behavior.

-----

<a id="model-experience"></a>
## Model Experience

### Operator persona

#### What the model sees

The preset contributes a complete Chinese EmbyMedia operator persona. It requires deterministic queries before claims, canonical plan-and-execute writes, Host-owned approval and verification, no secret collection in chat, and a strict distinction between `resource.add_new` and `series.update`.

#### Token effect

The fixed persona is present in every request for a session using this preset. Its size does not vary with media-library state; current facts arrive only through subsequent tool results.

#### KV Cache effect

The persona is installed before the session's first request and remains prefix-stable for that session. Selecting another preset creates a different prefix for a different session rather than rewriting a running session.

### Tool and skill composition

#### What the model sees

The model receives the EmbyMedia `embymedia_*` tools, `skill`, and `ask_user_question`, plus a compact catalog of eight EmbyMedia skills. A `skill` call loads the selected skill body for the task; excluded general-purpose capabilities never enter the composition.

#### Token effect

Tool schemas and the skill catalog add a fixed base cost. A selected skill and data-dependent EmbyMedia tool results add tokens only after the model invokes those entries.

#### KV Cache effect

The base tool and skill roster is stable for the session. Loading a skill or receiving a tool result changes the later conversation suffix without changing the already-established persona and schema prefix.

## Known Limitations and Deferred Work

<a id="known-limitations-and-deferred-work"></a>

These constraints describe what this packaged preset cannot supply on its own.

- **The Host service is external** — the preset mounts only the `/tools` consumer; sessions fail to activate those tools unless the surrounding Host composition already provides `@embymedia/dsh-operations` and its dependencies.
- **The persona is fixed and Chinese-first** — administrator scope, tool policy, and response language are asset text rather than library configuration; a deployment needing a different operator policy must own another preset.
- **The capability set is deliberately narrow** — shell, filesystem tools, web access, MCP, dynamic Cordis, subagents, and workflows are absent, so tasks requiring them need another trusted composition.
- **Skill updates follow packaged assets** — the library exposes no authoring or patch interface; consumers receive the eight skill directories present in the installed package.
- **No profile installation surface** — the package declares no bundle patch and exports no executable; it is useful only through an agent-preset consumer that accepts `PRESET_ROOT`.

<a id="dev-note"></a>
### Dev Note

<details>
<summary>Working context for maintainers — click to expand</summary>

None.

</details>
