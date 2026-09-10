# EmbyMedia 2.1.0

English | [中文](README.zh.md)

## Summary

EmbyMedia manages 115 accounts and files, CloudDrive2 mounts, Emby libraries, STRM output, and persistent media tasks through a Vue browser application, REST, OpenAPI, and MCP. One Go binary embeds the browser application. Production requires neither Node.js nor DeepSeek API credentials.

## Table of Contents

- [Build and run](#build-and-run)
- [Operate safely](#operate-safely)
- [Learn more](#learn-more)
- [License](#license)

## Build and run

Install Go 1.26, Node.js 22.19.0, pnpm 11.7.0, Python 3, and Make. Run these commands from the repository root; the independent web project owns its lockfile.

```sh
make install-web
make build
make test
make check
./bin/embymedia -db /tmp/embymedia-dev.db
```

`make install-web` installs frozen web dependencies. `make build` builds Vue assets and the Go binary. `make test` runs Go race tests and deployment Python tests; `make check` runs Vue typechecking, Go vet, and documentation checks. These targets build the embedded web assets as needed. Use a new disposable database for development; never reuse a production database.

Open `http://127.0.0.1:8080`. HTTP serves the browser application, `/api/v1/*`, `/api/v1/openapi.json`, and `/mcp`; legacy MCP SSE uses loopback port 8081. Configure provider accounts and service endpoints before running media operations. A browser shell or discovered tool does not prove that an external provider is connected.

The service also supports `-mcp` for stdio and `-check-db` for opening and migrating an isolated database without starting workers. Each database permits one owning process. Connect clients to the running server's HTTP MCP endpoint rather than starting stdio against its database.

## Operate safely

The Debian deployment runs `embymedia-v2.service` from `/opt/embymedia-v2/current`, with its database at `/srv/embymedia/data/embymedia.db`. Caddy and the HTTP login service protect browser access; Agent clients use separately named tokens. Emby and CloudDrive2 remain external services. See [operations](docs/operations.md) before installing a release or changing access.

Back up before production changes. Use the online snapshot backup path, not a raw copy of a live SQLite database and its WAL/SHM files. Backups include the browser-user database and generated STRM catalog; they do not replace an independent copy of original media or safe retention of recovery secrets. Restore into a fresh isolated directory and validate it before any separately authorized production recovery.

Keep provider cookies, API keys, Agent tokens, login secrets, databases, and original media out of source-control cleanup. Preserve Emby's shared STRM filesystem ACLs. Destructive operations require their own authorization; interrupted or partially completed work requires provider-state inspection before retry. [Safety](SAFETY.md) explains these limits.

## Learn more

- [Architecture](docs/architecture.md): runtime ownership and data flow.
- [Development](docs/development.md) and [testing](docs/testing.md): local workflow and verification scope.
- [Operations](docs/operations.md): deployment, Agent access, backup, and recovery.
- [Product](PRODUCT.md) and [design](DESIGN.md): media workflows and product design.
- [Agent Notes](.agents/notes/README.md): current decisions and frozen historical records.

## License

[MIT](LICENSE). Preserve the existing copyright notice and the [third-party notices](THIRD_PARTY_NOTICES.md).
