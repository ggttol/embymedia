# Agent Note: EmbyMedia workspace writes run without approval prompts

Status: implemented
Archived: 2026-09-10

English | [中文](2026-09-01-embymedia-unprompted-workspace-writes.zh.md)

## Problem

The EmbyMedia deployment requires repeated explicit approval for ordinary operations even though its operator has enabled business writes. The prompt pauses single-operator media work without adding a separate review authority.

## Decision

`deploy/cordis/embymedia.patch.yml` sets the composed approval policy to `never` and defines `workspace-write` as the default permission preset with `approval: never`. New EmbyMedia sessions retain the workspace-write sandbox while operations, including high-risk plan execution, proceed without UI approval cards. Durable plan preview, canonical plan hashing, target revalidation, execution audit, independent verification, and partial terminal states remain in force.

## Alternatives considered

**Keep per-operation approval.** Rejected because the deployment has one operator who explicitly accepted unattended execution and the repeated card interrupted routine work.

**Select `danger-full-access`.** Rejected because suppressing approval does not require broadening filesystem access beyond the existing workspace-write sandbox.

## Consequences

The operator can execute destructive or costly media actions without a human approval pause. Plan and verification records remain the only in-band review trail, so the deployment relies on its authenticated single-operator boundary and existing operation safeguards.