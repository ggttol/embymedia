# AGENTS.md

EmbyMedia V2 is a standalone Go server with an embedded Vue application. Read [docs/architecture.md](docs/architecture.md) before changing the runtime and [docs/operations.md](docs/operations.md) before changing deployment, backup, or restore behavior.

## Layout

- `cmd/server/`: HTTP/MCP entry point, database ownership lock, embedded frontend.
- `internal/`: API, MCP, domain, providers, persistent tasks, authorization, audit, SQLite.
- `web/`: independent Vue/TypeScript application and pnpm lockfile.
- `deploy/`: Debian release installer, systemd, Caddy, Emby/CloudDrive, login, media maintenance, backup and isolated restore tools.
- `docs/`: current architecture, development, testing, and operations documentation.
- `.agents/notes/`: current decisions; `archived/` contains immutable historical records, not current instructions.

## Build and verification

Use Go from `go.mod`, Node from `.node-version`, pnpm from `web/package.json`, and Python 3 for deployment tools. No root Node workspace or DeepSeek API key is required.

```sh
make install-web  # frozen frontend dependency installation
make build        # build the frontend and bin/embymedia
make check        # frontend typecheck, go vet, documentation checks
make test         # Go tests and deployment Python tests
make build-linux  # standalone Linux/amd64 release binary and checksum
```

Run the narrowest checks that cover the change; a build/deployment cleanup requires a clean-source build and deployment/restore tests. Never run tests against production credentials, databases, or media. Real-provider verification requires explicit authorization and a bounded operation. Report only verification actually executed. Do not claim a local fixture proves production restoration.

## Engineering rules

- Keep Go/Vue V2 independent. Do not reintroduce Cordis, DSH, legacy SDKs, PostgreSQL, or a Node production runtime.
- Preserve the single-owner database lock, migrations, durable task/execution state, cancellation, audit, authorization, and bounded shutdown behavior.
- Validate external inputs at HTTP/MCP, file, database, configuration, and provider-response entry points; fail with the actual error rather than synthetic success.
- Never log credentials or commit local databases, tokens, backups, environment files, or generated frontend/binary artifacts.
- Destructive media changes require the existing authorization and confirmation flow; do not weaken it for convenience. Automatic completed-pack replacement must preserve its verified identity and episode-coverage safeguards.
- Keep source changes and every affected caller together. Prefer existing patterns and maintained dependencies; avoid compatibility shims and unrelated refactors.
- Keep comments concise and local: explain failure, ownership, timing, security, and non-obvious constraints, not code narration or review history.
- Tests defend observable behavior and plausible regressions. Do not pin implementation details or weaken assertions to make a change pass.
- Keep existing Chinese UI copy, routes, and design unless the user requests a product change. Verify changed browser interactions in the actual application, including narrow layouts and keyboard use.
- Runtime configuration belongs in validated settings or documented environment variables, not hardcoded deployment-specific constants.

## Deployment safety

The supported release path is `/opt/embymedia-v2/current`. Preserve atomic activation, rollback to a previous V2 release, service-user ownership, STRM ACLs, CloudDrive/Emby recovery checks, and online SQLite backup consistency. Legacy-service retirement may disable old services but must never start them during rollback. Do not modify production services, local secrets, databases, or backups as part of source cleanup.

## Documentation and history

Update affected English/Chinese documentation together; follow `docs/AGENTS.md`. Non-trivial decisions belong in `.agents/notes/`, not comments describing the development session. Existing archive files and their recorded hashes are frozen; historical outbound links are not current dependencies. `scripts/check-docs.py` checks active links/pairs and archived integrity. Preserve `LICENSE` and applicable third-party notices when removing code.

`CLAUDE.md` points to this file. Edit `AGENTS.md`, not a duplicate. Files end with exactly one newline. Do not commit or push without user authorization.
