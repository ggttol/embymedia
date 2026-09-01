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

No third-party logo, customer, price, performance guarantee, or unsupported capability may be added.
