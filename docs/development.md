# EmbyMedia development

English | [中文](development.zh.md)

## Summary

Build and change the standalone Go server and Vue application without installing a root npm workspace or connecting to production services.

## Table of Contents

- [Local workflow](#local-workflow)
- [Change ownership](#change-ownership)

## Local workflow

Install the tool versions in the [quickstart](../README.md#build-and-run). Run `make install-web` once after checkout or lockfile changes, then `make build`, `make test`, and `make check`. The Makefile stages Vue output before Go compiles the embedded application; direct Go commands on a clean checkout need those assets first.

Run `./bin/embymedia -db /tmp/embymedia-dev.db` against a disposable database. Default HTTP and legacy SSE listeners are `127.0.0.1:8080` and `127.0.0.1:8081`; `-host`, `-port`, `-mcp-host`, `-mcp-port`, and `-db` select explicit addresses and state. Do not expose a development instance as a production login endpoint.

For interactive frontend development, `pnpm --dir web dev` starts Vite; its proxy configuration is owned by [`web/vite.config.ts`](../web/vite.config.ts). Keep the backend running separately with isolated state. Node.js is a build/development dependency, not a deployed application runtime.

`make build-linux` produces `bin/embymedia-linux-amd64` and its `.sha256` file. Artifact construction does not install or activate a release. [Operations](operations.md) owns that separately authorized procedure.

## Change ownership

Keep provider behavior in `internal/service`, durable formats in `internal/storage`, and transport authorization in their existing Go owners. Preserve the production database format and recovery paths during source cleanup. Update both languages of affected docs and keep their pairing records current under [the documentation rules](AGENTS.md).

Keep new tests focused on observable behavior, error states, ordering, and security. Provider fixtures must not use live cookies or mutate media. Record durable rationale in [Agent Notes](../.agents/notes/README.md); archived notes are historical, not current instructions. See [testing](testing.md) for verification scope.
