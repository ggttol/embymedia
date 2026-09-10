# EmbyMedia testing

English | [中文](testing.zh.md)

## Summary

Verify the Go service, browser build, and deployment tooling without provider credentials. Live media acceptance is a separate, explicitly authorized operation.

## Table of Contents

- [Repository checks](#repository-checks)
- [Behavior and production evidence](#behavior-and-production-evidence)

## Repository checks

After `make install-web`, run `make test` for Go race tests and Python deployment tests, and `make check` for Vue typechecking, Go vet, and the documentation checker. `make build` verifies the embedded browser/server artifact; `make build-linux` prepares the Linux deployment artifact and checksum. These targets own web-asset preparation so the clean checkout does not depend on committed build output.

CI covers Go race tests, Vue typechecking and build, deployment Python tests, and the Linux artifact. It requires no DeepSeek key, external model service, production database, or media-provider credentials. A local successful check does not prove Emby, CloudDrive2, or 115 availability.

## Behavior and production evidence

Go tests belong beside their owning packages. Deployment tests under [`deploy/scripts`](../deploy/scripts) use isolated fixtures for login, snapshots, release behavior, media normalization, and library cutover. Keep plausible failures observable: authorization denial, identity drift, interrupted work, stale mounts, and partially completed provider effects.

For a bug fix, preserve a regression only when it fails for a plausible bug. Do not pin incidental wording, source text, mocks forwarding values, or implementation layout. For browser changes, exercise the actual running Vue application with disposable data; compilation alone cannot establish keyboard, layout, or workflow behavior.

Never run destructive live media operations as an automated verification shortcut. Read provider results and persisted task outcomes; discovery is not execution, and accepted background work is not completion. A production backup and isolated restore are recovery evidence, not authorization to overwrite the running service. [Operations](operations.md) and [safety](../SAFETY.md) own those limits.
