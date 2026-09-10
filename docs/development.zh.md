# EmbyMedia 开发

[English](development.md) | 中文

## 概要

构建和修改独立 Go 服务与 Vue 应用，无需安装根 npm workspace，也无需连接生产服务。

## 目录

- [本地流程](#本地流程)
- [变更归属](#变更归属)

## 本地流程

安装[快速入门](../README.zh.md#构建与运行)规定的工具版本。检出或锁文件变化后先运行一次 `make install-web`，再运行 `make build`、`make test` 与 `make check`。Makefile 在 Go 编译内嵌应用前准备 Vue 输出；干净检出中直接运行 Go 命令也需要先准备这些资源。

使用 `./bin/embymedia -db /tmp/embymedia-dev.db` 操作可丢弃数据库。HTTP 与旧式 SSE 默认监听 `127.0.0.1:8080` 和 `127.0.0.1:8081`；`-host`、`-port`、`-mcp-host`、`-mcp-port` 和 `-db` 可指定地址与状态。不要把开发实例暴露为生产登录端点。

交互式前端开发使用 `pnpm --dir web dev` 启动 Vite；其代理配置由 [`web/vite.config.ts`](../web/vite.config.ts) 定义。后端需另行启动并使用隔离状态。Node.js 是构建及开发依赖，不是部署中的应用运行时。

`make build-linux` 生成 `bin/embymedia-linux-amd64` 及其 `.sha256` 文件。构建 artifact 不会安装或激活 release。[运维指南](operations.zh.md)负责需要单独授权的安装流程。

## 变更归属

provider 行为保留在 `internal/service`，持久格式保留在 `internal/storage`，传输身份验证由现有 Go 模块负责。源码清理期间保持生产数据库格式与恢复流程。按[文档规则](AGENTS.md)同步修改受影响文档的两种语言并更新配对记录。

新测试应针对可观测行为、错误状态、顺序与安全。provider fixture 不能使用真实 Cookie 或修改媒体。持久决策理由记入 [Agent Notes](../.agents/notes/README.zh.md)；归档记录属于历史，不是当前指令。验证范围见[测试指南](testing.zh.md)。
