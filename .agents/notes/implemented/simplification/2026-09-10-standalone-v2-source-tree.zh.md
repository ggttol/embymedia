# Agent Note: Keep only the standalone V2 source tree

Status: implemented

[English](2026-09-10-standalone-v2-source-tree.md) | 中文

## Problem

仓库在独立 Go 媒体应用旁保留通用 DeepSeek Harness/Cordis runtime。其插件图、语言 SDK、应用构建、生成手册与依赖模型的 CI 增加安装和维护成本，却不服务于支持的媒体 runtime。两套指令还容易让开发者构建错误应用，或误以为媒体操作需要模型凭据。

## Decision

支持的源码是 `cmd/server`、`internal`、独立 `web` pnpm 项目，以及当前部署、备份、还原、登录与媒体维护工具。`make install-web` 执行冻结前端安装；`make build` 构建 Vue 与 Go 可执行文件。`make test` 覆盖 Go 竞态测试及部署 Python 测试；`make check` 覆盖 Vue 类型检查、Go vet 与文档。`make build-linux` 生成 Linux 二进制及校验和。CI 无需 DeepSeek key 即可执行这些 V2 流程。

通用 Harness/Cordis 应用、SDK、vendored runtime、构建工具、CI、生成目录与手册不属于支持的产品。历史已实现记录移入冻结归档，不再作为常驻指令。过时的 workspace-write 审批记录属于历史；V2 的 token scope、浏览器拥有的危险操作开关、目标绑定删除审批及 Emby 原生管理员检查仍是当前权威。

Harness 事件 schema、取消接口、会话投影、动态插件、输入框行为、压缩、任务界面、SDK 发布、包清单及 Harness 专用测试等过时提案组，因其生产所有者被移除而否决。其记录与既有 Harness 专用否决记录被删除，因为它们无法防止独立媒体应用中的合理误区。通用文档与确定性测试目标保留在简洁的当前指南中，而不是未实现的 Harness 项目中。

源码裁剪不改变 `/opt/embymedia-v2/current`、生产 SQLite 格式、Emby／CloudDrive 集成、浏览器身份、STRM ACL 或备份／还原格式。它不授权部署、凭据轮换、数据库或媒体删除，也不授权清理被忽略的本地文件。现有版权与适用依赖归属声明仍须保留。当前媒体身份、恢复、规范化与访问决策继续保持活跃；现有归档 artifact 与封印保持不可变。

## Alternatives considered

**保留未使用 runtime 作为根 workspace。** 原插件架构支持可替换模型 provider、持久对话回放、可扩展工具与客户端 SDK。这些能力对 Agent 平台有价值，但会给并不调用它们的媒体服务增加另一产品的工具链与维护义务。

**保留全部活跃手册及决策，只将 runtime 标为可选。** 这会保留相互冲突的构建、安全与凭据指令。冻结历史记录能够保存理由，而不把已删除能力呈现为受支持功能。

**重写或删除历史归档。** 这会丢失已封印的决策证据并破坏归档完整性保证。新归档追加封印；旧记录作为历史快照保持字节不变。

**在源码清理时替换生产数据库或重新部署。** 删除未使用源码不需要这两项操作。保留生产状态并要求单独授权 release，可避免仓库维护与不可逆媒体副作用耦合。

## Consequences

此检出放弃开发及分发通用 Agent 平台、其 SDK 与模型回放测试。重新引入这些能力需要明确产品决策、已证明的 V2 消费者、独立依赖所有权，以及相应安全与恢复验证；仅凭历史记录不能成为恢复 monorepo 的理由。

要求的验证是干净源码冻结 web 安装、V2 构建、竞态／部署测试、类型／vet／文档检查及 Linux artifact 构建。现有媒体测试仍须覆盖源身份、破坏性授权、任务中断与备份／还原行为。这些要求不声称已经进行真实部署或破坏性验收。冻结记录保留原有历史证据，包括属于已移除代码的测试与路径。
