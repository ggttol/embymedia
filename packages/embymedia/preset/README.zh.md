---
description: "为通过 dsh-agent-presets 加载聚焦 persona、EmbyMedia 工具与八个运营 skill 的组合提供打包的 Emby operator preset 资产与入口常量。"
kind: "package-library"
---

# @embymedia/dsh-preset

[English](README.md) | 中文

## 概述

`dsh-preset` 向 agent-preset 消费方提供打包的 `emby-operator` 组合及其所在文件系统根目录。使用该 preset 的会话会获得中文 EmbyMedia 运营 persona、EmbyMedia 工具入口、skill 发现、skill 工具、ask-user 工具与八个聚焦的运营 skill。该组合刻意排除 shell、文件系统、Web、MCP、动态 Cordis、subagent 与 workflow 能力。本包是纯资产与常量库：它没有 Cordis 插件入口、profile patch 或包 bin。

## 目录

- [使用本包](#use-this-package)
- [理解实现](#understand-the-implementation)
- [进一步探索](#further-exploration)
- [模型体验](#model-experience)
- [已知限制与延期工作](#known-limitations-and-deferred-work)
- [开发备注](#dev-note)

-----

<a id="use-this-package"></a>
## 使用本包

从配置 `@deepseek-ai/dsh-agent-presets` 的 Host 组合消费导出的根目录与 preset ID；不要把本包挂载为 Cordis 配置项，也不要把它当作可安装 profile 层。

### 何时使用

当部署已经挂载 EmbyMedia Host 服务，并且需要带产品工具、审批感知指令与领域 skill 的窄范围运营会话时，使用此包。当 agent 需要通用 shell、文件系统、Web、委派或 workflow 能力，或者 persona 与信任策略必须由部署配置时，请使用其他 preset。

### 入口

入口导出打包 preset 目录及其稳定 preset ID：

```text
import { EMBY_OPERATOR_PRESET, PRESET_ROOT } from '@embymedia/dsh-preset'

const agentPresetConfig = {
  default: EMBY_OPERATOR_PRESET,
  includeShippedRoot: false,
  includeUserRoot: false,
  roots: [{ path: PRESET_ROOT, trust: 'system' }],
}
```

把该配置传给组合中已有的 `@deepseek-ai/dsh-agent-presets` 配置项。成功表示其 roster 能从 `PRESET_ROOT` 发现 `emby-operator`；缺少资产、配置项无法加载或依赖不可用由 preset 消费方报告，而不是由本库报告。本包不应出现 package-bin 或 `dsh plugin` 命令。

### Preset 组合内容

Preset 在一个会话作用域组合中挂载五个配置项：完整运营 persona、`@embymedia/dsh-operations/tools`、文件系统支持的 skill 发现、`skill` 工具与 `ask_user_question`。Host 级 `@embymedia/dsh-operations` 服务位于本包之外，且必须已经对工具配置项可用。

八个打包 skill 分别覆盖媒体库运营、新资源入库、既有 Series 跟进、元数据修复、清理与去重、用户策略与设置、备份与恢复以及事故诊断。Skill description 决定模型何时加载各自正文；README 不重复完整流程。

### 运营策略

Persona 把每条最新用户请求视为独立任务，要求先用确定性工具读取事实，并把写入路由为一个 canonical 计划，再由 Host 拥有执行、审批与验证。普通删除只使用 `list_items` 和一个 `media.delete` 计划；除非请求本身需要，否则不会向用户暴露 dedup、scan、retry、存储根或恢复选择。重复 blocker 与操作终态会结束任务，而不是启动探索性工具循环。在 `safe-auto` 模式下，只有 Host 的有界安全列表无需审批即可继续；破坏性与替换操作仍只需一次审批。

既有 Series 的缺集通常先使用 `embymedia_series` 的 gap 与 resource-plan 查询，再用 canonical 媒体库与 Series ID 以及显式请求集数调用 `series.update`。已完结 Series 没有逐集候选时，运营方可以把完整整包作为临时新根入库，证明新的相同 TMDB Series 已无缺集后，再执行单独审批的 `dedup.delete`，保留新 Series 并删除全部旧的不完整根。最终验证使用 canonical Host 事实，且绝不依赖 115 访问码。

-----

<a id="understand-the-implementation"></a>
## 理解实现

<details>
<summary>实现细节——点击展开</summary>

运行时入口相对构建后模块计算 `PRESET_ROOT`，并把 `EMBY_OPERATOR_PRESET` 导出为目录 ID。Agent-preset 服务拥有发现与挂载；本包只拥有资产树及其内部组合。每个 skill 保持为独立文件系统单元，使模型只加载与当前任务有关的指令。

| 文件 | 职责 |
|---|---|
| [`src/index.ts`](src/index.ts) | 打包 preset 根目录与稳定 preset ID 导出 |
| [`presets/emby-operator/preset.yml`](presets/emby-operator/preset.yml) | Roster 展示元数据 |
| [`presets/emby-operator/agent.cordis.yml`](presets/emby-operator/agent.cordis.yml) | Persona 与会话作用域插件组合 |
| [`presets/emby-operator/skills/`](presets/emby-operator/skills/) | 通过 skill 工具加载的八个运营 skill 包 |

</details>

-----

<a id="further-exploration"></a>
## 进一步探索

当打包组合不足以回答问题时阅读以下页面。它们从 preset 挂载进入 Host 操作行为与浏览器展示。

- [Agent preset](../../preset/agent-presets/README.zh.md)——roster 根目录、信任、会话组合与失败处理。
- [EmbyMedia 操作](../operations/README.zh.md)——canonical 规划、审批、执行与验证。
- [EmbyMedia 客户端 UI](../ui/README.zh.md)——与运营 agent 配套的卡片和 Remote 支撑工作台。
- [Cordis 入门](../../../docs/cordis-primer.zh.md)——组合配置项与注入行为。

-----

<a id="model-experience"></a>
## 模型体验

### 运营 persona

#### 模型看到什么

Preset 提供完整的中文 EmbyMedia 运营 persona。它要求在作出判断前查询确定性事实、写入必须经过 canonical 计划与执行、审批与验证由 Host 拥有、聊天中不得收集 secret，并严格区分 `resource.add_new` 与 `series.update`。

#### Token 影响

使用此 preset 的会话每次请求都包含固定 persona。其大小不会随媒体库状态变化；当前事实只通过后续工具结果进入。

#### KV Cache 影响

Persona 在会话第一次请求前安装，并在该会话中保持前缀稳定。选择其他 preset 会为另一个会话建立不同前缀，而不是重写运行中会话。

### 工具与 skill 组合

#### 模型看到什么

模型会收到 EmbyMedia `embymedia_*` 工具、`skill`、`ask_user_question`，以及八个 EmbyMedia skill 的紧凑目录。`skill` 调用为任务加载选中的 skill 正文；被排除的通用能力不会进入组合。

#### Token 影响

工具 schema 与 skill 目录增加固定基础成本。选中的 skill 与数据相关 EmbyMedia 工具结果只会在模型调用对应入口后增加 token。

#### KV Cache 影响

基础工具与 skill roster 在会话中稳定。加载 skill 或收到工具结果会改变后续对话后缀，而不会改变已建立的 persona 与 schema 前缀。

## 已知限制与延期工作

<a id="known-limitations-and-deferred-work"></a>

这些约束说明此打包 preset 无法单独提供什么。

- **Host 服务位于外部**——preset 只挂载 `/tools` 消费方；除非外围 Host 组合已经提供 `@embymedia/dsh-operations` 及其依赖，否则会话无法激活这些工具。
- **Persona 固定且中文优先**——管理员范围、工具策略与响应语言是资产文本，而不是库配置；需要不同运营策略的部署必须拥有另一个 preset。
- **能力集合刻意保持狭窄**——shell、文件系统工具、Web 访问、MCP、动态 Cordis、subagent 与 workflow 均不存在，因此需要它们的任务必须使用其他受信组合。
- **Skill 更新跟随打包资产**——本库不暴露创作或 patch 接口；消费方接收已安装包中存在的八个 skill 目录。
- **没有 profile 安装入口**——本包未声明 bundle patch，也不导出可执行文件；它只能通过接受 `PRESET_ROOT` 的 agent-preset 消费方发挥作用。

<a id="dev-note"></a>
### 开发备注

<details>
<summary>维护者的工作上下文——点击展开</summary>

None.

</details>
