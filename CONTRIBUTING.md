# Contributing to EmbyMedia

English | [中文](CONTRIBUTING.zh.md)

## Summary

Contribute to the Go service, Vue application, or deployment tools while preserving production data and media-operation safety.

## Table of Contents

- [Workflow](#workflow)
- [Review requirements](#review-requirements)

## Workflow

Follow [development](docs/development.md). Install frozen frontend dependencies with `make install-web`, build with `make build`, and use `make test` and `make check` before submitting a change. Keep changes scoped and describe only verification actually performed.

Use disposable databases and provider fixtures. Source changes do not authorize deployment, database deletion, credential changes, or live media mutation. [Safety](SAFETY.md) and [operations](docs/operations.md) define those constraints.

## Review requirements

Preserve shared authorization, task durability, source identity checks, STRM ACLs, and backup/restore compatibility. Update affected English/Chinese docs together and record non-trivial decisions in [Agent Notes](.agents/notes/README.md). Do not edit frozen archived notes.

Include evidence from the actual browser for UI changes and a plausible regression for bug fixes. Do not add tests that only assert wording or implementation layout. Keep the existing [license](LICENSE) and applicable third-party attribution when moving or removing source.
