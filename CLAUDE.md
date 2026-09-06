# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

EmbyMedia V2 is a self-hosted operations system for managing 115 Cloud Drive, CloudDrive2, Emby, STRM generation, background task queues, REST APIs, OpenAPI, and Model Context Protocol (MCP). The production runtime is a single standalone Go binary embedding the compiled Vue frontend.

## Architecture

```
cmd/server/          # Main entrypoint, HTTP/MCP listeners, and //go:embed for web/dist
internal/
  api/               # Echo REST endpoints, middleware, audit query, and OpenAPI 3.1 generator
  service/           # Business logic: 115 drive, Emby REST, CloudDrive2 gRPC, STRM sync/verify, task queue, cron
  storage/           # SQLite repository using modernc.org/sqlite (settings, tasks, tokens, audits)
  security/          # SHA-256 token hashing, request authorization, and audit log generation
  mcp/               # Model Context Protocol server exposing operational tools via Streamable HTTP, SSE, and stdio
  clouddrivepb/      # Generated Protobuf / gRPC client for CloudDrive2
  domain/            # Core models and domain types
web/                 # Frontend SPA (Vue 3, Vite, Tailwind CSS, TypeScript, Lucide icons)
deploy/              # Systemd unit templates, scripts, and deployment configurations
```

### Core Architectural Invariants

- **Single Binary Deployment**: The Go backend embeds `web/dist` directly (`cmd/server/web.go`). Node/pnpm is only needed for building the frontend asset bundle.
- **SQLite Persistence**: Stores all settings, managed 115 accounts, cron schedules, task attempts, agent tokens, and audit logs. Uses `modernc.org/sqlite` (pure Go, CGO-free). Default database path is `/srv/embymedia/data/embymedia.db`.
- **Protocol Unification**: `cmd/server` serves the Vue SPA, REST API (`/api/v1/*`), OpenAPI specification (`/api/v1/openapi.json`), and Streamable HTTP MCP (`/mcp`) on a single port (`3080`). Legacy SSE listens on `3081`, and `-mcp` serves MCP over stdio.
- **Authentication**: Public browser traffic is authenticated via an upstream reverse proxy (Caddy). API & MCP requests carrying `X-Agent-Token` or `Authorization: Bearer <token>` bypass proxy login and are validated directly by the Go backend. Stored tokens are SHA-256 digests.
- **STRM Generation & Canary Protection**: STRM sync traverses 115 mounts without following output symlinks and ensures generated directories are traversable. Pruning stale STRM files only occurs when the media-mount canary file is present to prevent accidental mass deletion if the cloud drive unmounts.
- **Persistent Task Queue & Audit**: Background operations run asynchronously with persistent task attempts, status, progress, logs, cancellation, and manual retry. Failed or partially invalid runs are stored as actionable findings rather than silent failures.

## Development Commands

### Go Backend

```bash
# Run all Go tests
go test ./...

# Run tests for a specific package
go test ./internal/service/...
go test ./internal/storage/...
go test ./internal/api/...

# Run a specific single test with verbose output
go test ./internal/service -v -run TestMediaServiceSynchronizesAndVerifiesSTRM
go test ./internal/api -v -run TestAPIRoutes
go test ./internal/storage -v -run TestStorageOperations

# Compile the Go server binary
go build -o bin/embymedia ./cmd/server

# Run the server locally
go run ./cmd/server -host 127.0.0.1 -port 3080 -db ./embymedia.db
```

### Frontend (web/)

```bash
# Navigate to frontend directory
cd web

# Install dependencies
pnpm install

# Run Vite development server (with HMR)
pnpm run dev

# Typecheck and build frontend into web/dist (for Go embed)
pnpm run build
```

### Full Build (Frontend + Backend)

```bash
# Build frontend assets, then build embedded Go binary
cd web && pnpm run build && cd .. && go build -o bin/embymedia ./cmd/server
```
