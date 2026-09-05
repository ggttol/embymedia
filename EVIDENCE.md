# EVIDENCE.md

| Claim | Source | Status | Allowed wording |
| --- | --- | --- | --- |
| Emby, CloudDrive2, PostgreSQL, and Authelia are healthy Docker services on Debian | Live container and systemd checks in this session | verified | “服务健康” when snapshot reports healthy |
| NPS is hosted at 192.168.2.3 | Completed migration and live checks | verified | exact address |
| DSH write mode is enabled and scheduler disabled | `/internal/embymedia/health` | verified | exact state values |
| Credentials are stored as write-only DSH records | `credentials.ts` | verified | configured/writable status only |
| Resource add-new has canonical plan, real executor, and verifier | Source, 80 tests, browser canonical-plan check | verified | “支持一条龙资源添加” |
| All 28 mutation kinds work | source/runtime map | blocked | omit; show supported/unsupported state |
| Hardware transcoding is available | deployment inventory | false | “软件转码” only |
| CloudDrive no longer depends on NAS | runtime network, mounts, DB/text audit | verified | “无运行时 NAS 依赖” |
| TLS/MFA protects public endpoints | user-selected HTTP topology | false | omit |
| Metrics represent live state | admin snapshot Remote | verified at snapshot time | label with generated time |
| Debian Hermes is version 0.21.0 and connects to the V2 MCP registry | `hermes mcp test embymedia` on gaotao.cc | verified | “Hermes v0.21.0” and “18 tools discovered” |
| Streamable HTTP MCP is available on the main service listener | Real `initialize` and `tools/list` exchange against `/mcp` | verified | “MCP protocol handshake succeeded” |
| Public port 3081 is reachable | External connection probe timed out while the on-host SSE probe succeeded | false | Describe 3081 as on-host legacy SSE only |
| OpenAPI alias returns a 3.1 schema | `/api/v1/openapi.json` response | verified | “OpenAPI 3.1.0” with the observed REST path count |

No third-party logo, customer, price, performance guarantee, or unsupported capability may be added.
