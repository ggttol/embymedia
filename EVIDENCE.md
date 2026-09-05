# EVIDENCE.md

| Claim | Source | Status | Allowed wording |
| --- | --- | --- | --- |
| Emby and CloudDrive2 are healthy Docker services on Debian | Live container health checks after the CloudDrive restart incident | verified | “服务健康” at the recorded check time |
| Emby sees the CloudDrive media mount | `/media/.embymedia-health-canary` read inside the Emby container | verified after Emby restart | “媒体挂载已重新接入”；do not claim a user playback succeeded without a playback check |
| The V2 runtime needs DSH, Node, or PostgreSQL | systemd/Compose cutover and standalone build | false | “生产 runtime 不依赖 DSH、Node 或 PostgreSQL” |
| All eighteen MCP handlers execute a service or durable operation | MCP registry review plus provider/task/storage tests | verified | “18 / 18 工具有真实实现路径” |
| 115 list, mkdir, rename, move, recycle, share receive/create, and offline download call provider endpoints | `TestDriveProviderOperations` | verified against protocol-compatible HTTP fixtures | Name provider configuration as a prerequisite |
| CloudDrive status and remount use official gRPC methods | generated version-matched subset plus `TestCloudDriveGRPCHealthAndRemount` | verified against a gRPC provider fixture | “gRPC 实现已验证”；live remount was not repeated after the playback incident |
| STRM synchronization rejects output symlinks and escaping targets | Media service containment tests | verified | “STRM 写入和验证限制在配置根目录内” |
| Persistent tasks support cancellation, attempts, logs, restart recovery, and explicit retry | task queue/storage tests | verified | Only `completed` is success |
| REST and HTTP MCP reject unauthenticated Agent traffic and enforce scopes/rates | API, security, Streamable MCP, and HTTP-wrapper tests | verified | Browser login and Agent token are distinct paths |
| Agent audit records redact nested credentials and contain bounded valid JSON | security and MCP policy tests | verified | “输入和输出摘要脱敏并受限” |
| Debian Hermes is version 0.21.0 and discovers the standalone registry | `hermes mcp test embymedia` on gaotao.cc | verified before final deployment refresh | Re-run after deployment before final claim |
| The V2 Hermes skill is enabled and stale DSH skills are absent | `hermes skills list --source local --enabled-only` | verified | “embymedia-v2-operator 已启用” |
| Streamable HTTP, stdio, and legacy SSE transports answer their protocol entry paths | real local binary probes | verified | Legacy SSE remains loopback-only |
| OpenAPI contains every registered API route with unique operation IDs | route-coverage test and Redocly validation | verified | State the observed path count only after final build |
| Automatic TMDB candidate selection is safe | no deterministic disambiguation source | false | Require explicit TMDB ID; do not claim automatic matching |

No third-party logo, customer, price, performance guarantee, successful playback observation, or unsupported capability may be added.
