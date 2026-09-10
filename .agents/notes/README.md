# Agent Notes

English | [中文](README.zh.md)

Agent Notes record current EmbyMedia decisions, their rationale, rejected alternatives, and consequences. Code and user guides own current behavior and procedures; a note should preserve decision value rather than duplicate implementation inventories.

## Layout and format

Active records use `{lifecycle}/{kind}/yyyy-mm-dd-topic.md`, with Chinese and `.i18n.yaml` siblings. Lifecycles are `proposed`, `implemented`, and `rejected`; kinds are `architecture`, `feature`, `bug-fix`, `simplification`, `process`, and `testing`. The filename date is the first proposal date. Do not create a central note index.

Start each language with `# Agent Note: <title>`, a blank line, and `Status: proposed`, `Status: implemented`, or `Status: rejected — <reason>`. The machine-readable prefix and status remain English. Every note starts its body with `## Problem` and records `## Alternatives considered`. Implemented records use `## Decision` and `## Consequences`; proposals use `## Proposal`, `## Acceptance criteria`, and `## Risks`.

Update or add a note for non-trivial behavior, architecture, shared contracts, tooling, testing strategy, or persistent-format changes. Keep implemented facts current without reversing the recorded decision in place. A reversal needs a new owner that states what it replaces and what remains protected. Follow [the documentation pairing rules](../../docs/AGENTS.md).

## Retention and supersession

Keep implemented rationale active when it still guides security, ownership, durable formats, alternatives, or reintroduction decisions. Archive an implemented triplet when its completed decision no longer constrains the standalone application. Word count and age are not retention criteria.

Reject an obsolete proposal; do not archive it. Delete a rejected triplet when its premise is gone and its rationale cannot prevent a plausible mistake. A fully superseded implemented note may be deleted only after the current owner absorbs every still-useful rationale, alternative, consequence, and verification gap. Keep partial supersessions cross-linked.

## Frozen history

[`archived/`](archived/AGENTS.md) contains historical snapshots, not current instructions. Existing archived language files, sidecars, and manifest entries are immutable. Never repair their outbound links or apply their removed-runtime assumptions to V2.

A new archival operation moves the complete triplet from `implemented/<kind>` to `archived/<kind>`, inserts identical `Archived: YYYY-MM-DD` lines directly below both status lines, mechanically updates the pair hashes, and appends raw-byte SHA-256 seals to [`archived/manifest.json`](archived/manifest.json). Do not change the archived body. Repair inbound links from active documents in the same change.

The manifest has `version: 1` and a `files` map from archive-relative filenames to `sha256:<hex>`. Preserve every existing key and hash. `make check` verifies archive integrity and active pairing through the Python documentation checker; no removed npm workspace or generator is needed.
