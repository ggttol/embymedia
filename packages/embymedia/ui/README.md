---
description: "Browser presentation for EmbyMedia operations: durable tool-call cards, write-only credential staging, and a Remote-backed read workspace that hands mutations to the current AI conversation."
kind: "package-reference"
---

# @embymedia/dsh-client-ui-operations

English | [中文](README.zh.md)

## Summary

`dsh-client-ui-operations` renders EmbyMedia tool calls as status and risk cards derived from the durable call/result record. It also provides a full-screen Emby operations workspace that reads Host snapshots and credential availability through product Remotes. Workspace mutation buttons do not write directly: they queue a canonical-plan request into the currently selected DSH conversation. The Host entry is intentionally empty; all behavior lives in the browser client entry and requires the matching operation Remotes and client UI services.

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

Mount this package in a Web composition that already supplies the session controller, locale, renderer, layout, tool-view slots, Remote client, and the EmbyMedia Host Remotes.

### When to choose it

Choose it when operators need readable EmbyMedia cards and a read-oriented workspace beside the AI conversation. Avoid it in headless compositions, with a different operation protocol, or when the Host does not expose `embymedia-admin` and `embymedia-secrets` Remotes.

### Minimal configuration

The plugin has no configuration fields. Its manifest declares the browser entry and client injections; the Host `apply()` is empty.

```yaml
- name: '@embymedia/dsh-client-ui-operations'
```

| Field | Default | Meaning |
|---|---|---|
| None | — | Browser behavior is fixed by the registered card and overlay slots |

The surrounding Web composition owns client module loading and all injected services. This package neither starts a browser application nor mounts the Host operation service.

### Tool cards

The client registers keyed views for the thirteen EmbyMedia tools named in [`src/tools.ts`](src/tools.ts). Each card parses only the durable tool arguments and result text, then shows the tool or operation title, running or terminal status, risk, stable identifiers, target count, and either canonical confirmation or structured query data. Localized labels retain canonical English values beside Chinese display text.

A malformed or non-object payload is not interpreted. The card falls back to a raw compatibility view so the operator can inspect the recorded arguments or result instead of seeing invented fields. When the common tool-view owner provides trajectory inspection, the card exposes that action unchanged.

A `config.credential_rotate` preview for an ordinary supported credential adds a password input. Submission sends the value to `embymedia-secrets.stage(sessionId, planId, value)` through the Remote, clears the local input after success, and never inserts the value into the rendered result. The card does not expose this input for the generated CloudDrive webhook secret.

### Remote-backed workspace

The `shell.overlay` registration portals the workspace to `document.body`, outside the width-constrained shell slot. It opens by default unless local browser state records it as closed, loads an `embymedia-admin.snapshot`, and refreshes the snapshot every 30 seconds while open. The workspace presents eight views: dashboard, libraries, resource onboarding, metadata, tasks, users, configuration, and audit.

Snapshot content is read-only presentation: health, libraries, users, task counts and recent rows, schedules, audit rows, masked settings, configured credential status, and the Host's supported-operation set. Credential checks call `embymedia-admin.credentialCheck` and render only availability, message, and latency returned by the Host.

Buttons that describe a write create a Chinese prompt for a canonical operation plan and queue it to the currently selected session. A successful handoff closes the workspace and returns the operator to the AI conversation for plan review and approval. No selected session, unavailable binding, Remote failure, or prompt rejection is shown as an error; the UI never falls back to a direct mutation.

-----

<a id="understand-the-implementation"></a>
## Understand the implementation

<details>
<summary>Implementation internals — click to expand</summary>

The Host package entry exists only so the Loader can select the package; browser registration lives in `src/client/index.ts`. One slot contribution maps stable tool names to `OperationCard`, and another installs `EmbyWorkspace` as a shell overlay. Both surfaces receive narrow callbacks that adapt Remote responses and the current session binding rather than importing Host implementation code.

| File | Role |
|---|---|
| [`src/index.ts`](src/index.ts) | Intentionally empty Host plugin entry |
| [`src/client/index.ts`](src/client/index.ts) | Client injections, card-slot registration, Remote adapters, and workspace registration |
| [`src/client/OperationCard.tsx`](src/client/OperationCard.tsx) | Durable call/result parsing, card rendering, fallback view, and credential staging input |
| [`src/client/EmbyWorkspace.tsx`](src/client/EmbyWorkspace.tsx) | Snapshot workspace, credential checks, and prompt handoff |
| [`src/tools.ts`](src/tools.ts) | Stable thirteen-tool card roster shared with the invariant |
| [`src/invariant.ts`](src/invariant.ts) | Card-roster uniqueness and cardinality check |

</details>

-----

<a id="further-exploration"></a>
## Further Exploration

Read these pages when presentation behavior is not enough. They move from the shared tool UI to the Host operations and preset that supply the data.

- [Client tool UI](../../client/ui-tool/README.md) — the shared tool-view props and slot ownership.
- [EmbyMedia operations](../operations/README.md) — Host Remotes, canonical plans, and verification semantics.
- [EmbyMedia preset](../preset/README.md) — the operator persona that receives plan prompts from the workspace.
- [Cordis primer](../../../docs/cordis-primer.md) — Host and Client plugin composition.

-----

<a id="model-experience"></a>
## Model Experience

### Browser-only presentation

#### What the model sees

Nothing is added by this package to model input. `EMBYMEDIA_CARD_TOOLS` selects browser renderers for already-logged calls, and workspace buttons submit ordinary user prompts through the existing session controller.

#### Token effect

Zero direct tokens. A workspace prompt consumes tokens only after the user chooses an action and the session controller accepts it as a user message; the prompt text is then ordinary conversation input.

#### KV Cache effect

Card rendering, snapshot refreshes, credential checks, and overlay state do not alter request prefixes. A queued workspace prompt appends to the conversation and affects only the subsequent request suffix.

## Known Limitations and Deferred Work

<a id="known-limitations-and-deferred-work"></a>

These limits define the package's browser and Remote dependencies.

- **Browser-only behavior** — the Host entry performs no registration; headless compositions receive no cards or workspace from this package.
- **Remote availability is mandatory** — the workspace cannot load or check credentials until `embymedia-admin` is ready, and card credential staging cannot succeed until `embymedia-secrets` is ready.
- **Snapshot refresh is polling** — the workspace refreshes on open and every 30 seconds while open; it does not subscribe to push updates between polls.
- **Writes require a selected conversation** — workspace actions only queue prompts; without a current usable session they fail visibly and never execute directly.
- **Cards require JSON object payloads** — malformed, non-object, or protocol-drifted results use the raw fallback view and lose structured labels.
- **Credential entry is intentionally narrow** — only ordinary `config.credential_rotate` plans receive an input; webhook-secret generation and every other secret flow stay Host-owned.

<a id="dev-note"></a>
### Dev Note

<details>
<summary>Working context for maintainers — click to expand</summary>

None.

</details>
