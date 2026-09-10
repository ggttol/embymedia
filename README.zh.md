# EmbyMedia 2.1.0

[English](README.md) | 中文

## 概要

EmbyMedia 通过 Vue 浏览器应用、REST、OpenAPI 与 MCP 管理 115 账号和文件、CloudDrive2 挂载、Emby 媒体库、STRM 输出及持久媒体任务。一个 Go 二进制内嵌浏览器应用。生产运行既不需要 Node.js，也不需要 DeepSeek API 凭据。

## 目录

- [构建与运行](#构建与运行)
- [安全运维](#安全运维)
- [进一步了解](#进一步了解)
- [许可证](#许可证)

## 构建与运行

安装 Go 1.26、Node.js 22.19.0、pnpm 11.7.0、Python 3 与 Make。在仓库根目录运行以下命令；独立 web 项目拥有自己的锁文件。

```sh
make install-web
make build
make test
make check
./bin/embymedia -db /tmp/embymedia-dev.db
```

`make install-web` 按冻结锁文件安装 web 依赖。`make build` 构建 Vue 资源与 Go 二进制。`make test` 运行 Go 竞态测试及部署 Python 测试；`make check` 运行 Vue 类型检查、Go vet 与文档检查。这些目标按需构建内嵌 web 资源。开发时使用新的可丢弃数据库，绝不复用生产数据库。

打开 `http://127.0.0.1:8080`。HTTP 提供浏览器应用、`/api/v1/*`、`/api/v1/openapi.json` 与 `/mcp`；旧式 MCP SSE 使用回环端口 8081。运行媒体操作前先配置 provider 账号与服务端点。能打开浏览器界面或发现工具，不代表外部 provider 已连接。

服务还支持通过 `-mcp` 提供 stdio，以及通过 `-check-db` 打开并迁移隔离数据库而不启动 worker。每个数据库只允许一个拥有进程。客户端应连接运行中服务的 HTTP MCP 端点，而不是对它的数据库另启 stdio。

## 安全运维

Debian 部署通过 `/opt/embymedia-v2/current` 运行 `embymedia-v2.service`，数据库位于 `/srv/embymedia/data/embymedia.db`。Caddy 与 HTTP 登录服务保护浏览器访问；Agent 客户端使用分别命名的令牌。Emby 与 CloudDrive2 仍是外部服务。安装 release 或修改访问权限前，请阅读[运维指南](docs/operations.zh.md)。

生产变更前先备份。使用在线快照备份流程，不要直接复制运行中的 SQLite 数据库及其 WAL/SHM 文件。备份包含浏览器用户数据库与生成的 STRM 目录，但不能替代原始媒体的独立副本，也不能替代恢复 secret 的安全保管。先还原至全新隔离目录并验证，再进行另行授权的生产恢复。

provider Cookie、API key、Agent token、登录 secret、数据库与原始媒体不属于源码清理范围。保留 Emby 共享 STRM 文件系统 ACL。破坏性操作需要单独授权；重试中断或部分完成的工作前，必须检查 provider 状态。[安全说明](SAFETY.zh.md)解释这些限制。

## 进一步了解

- [架构](docs/architecture.zh.md)：运行时所有权与数据流。
- [开发](docs/development.zh.md)与[测试](docs/testing.zh.md)：本地流程及验证范围。
- [运维](docs/operations.zh.md)：部署、Agent 访问、备份与恢复。
- [产品](PRODUCT.md)与[设计](DESIGN.md)：媒体工作流及产品设计。
- [Agent Notes](.agents/notes/README.zh.md)：当前决策与冻结的历史记录。

## 许可证

[MIT](LICENSE)。保留现有版权声明与[第三方声明](THIRD_PARTY_NOTICES.zh.md)。
