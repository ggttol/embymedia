---
description: "EmbyMedia 操作的浏览器展示：持久工具调用卡片、只写凭据暂存，以及把变更交给当前 AI 对话的 Remote 支撑只读工作台。"
kind: "package-reference"
---

# @embymedia/dsh-client-ui-operations

[English](README.md) | 中文

## 概述

`dsh-client-ui-operations` 把 EmbyMedia 工具调用渲染为从持久调用／结果记录派生的状态与风险卡片。它还提供全屏 Emby 运营工作台，通过产品 Remote 读取 Host 快照与凭据可用性。工作台变更按钮不会直接写入：它们会把 canonical 计划请求排入当前选中的 DSH 对话。Host 入口刻意为空；所有行为都位于浏览器客户端入口，并依赖匹配的操作 Remote 与客户端 UI 服务。

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

在已经提供会话控制器、locale、renderer、layout、工具视图 slot、Remote 客户端与 EmbyMedia Host Remote 的 Web 组合中挂载此包。

### 何时选择

当运营方需要 AI 对话旁边的可读 EmbyMedia 卡片与读取导向工作台时选择它。Headless 组合、使用不同操作协议的组合，或 Host 未暴露 `embymedia-admin` 与 `embymedia-secrets` Remote 时不应选择它。

### 最小配置

插件没有配置字段。其 manifest 声明浏览器入口与客户端注入；Host `apply()` 为空。

```yaml
- name: '@embymedia/dsh-client-ui-operations'
```

| 字段 | 默认值 | 含义 |
|---|---|---|
| 无 | — | 浏览器行为由已注册卡片与 overlay slot 固定 |

外围 Web 组合拥有客户端模块加载与所有注入服务。本包既不启动浏览器应用，也不挂载 Host 操作服务。

### 工具卡片

客户端为 [`src/tools.ts`](src/tools.ts) 命名的十三个 EmbyMedia 工具注册 keyed view。每张卡片只解析持久工具参数与结果文本，然后展示工具或操作标题、运行中或终态状态、风险、稳定 ID、目标数量，以及 canonical confirmation 或结构化查询数据。Localized label 会在中文展示文本旁保留 canonical English 值。

格式错误或非对象 payload 不会被解释。卡片退回原始兼容视图，让运营方检查记录的参数或结果，而不是看到臆造字段。当通用工具视图拥有方提供轨迹检查时，卡片会原样暴露该操作。

普通受支持凭据的 `config.credential_rotate` 预览会增加 password 输入框。提交通过 Remote 把值发送到 `embymedia-secrets.stage(sessionId, planId, value)`，成功后清空本地输入，且绝不会把值插入渲染结果。卡片不会为自动生成的 CloudDrive webhook secret 暴露此输入框。

### Remote 支撑工作台

`shell.overlay` 注册把工作台 portal 到 `document.body`，位于宽度受限 shell slot 之外。除非浏览器本地状态记录为关闭，否则它默认打开，加载一个 `embymedia-admin.snapshot`，并在打开期间每 30 秒刷新快照。工作台提供八个视图：总览、媒体库、资源入库、元数据、任务、用户、配置与审计。

快照内容只用于只读展示：健康、媒体库、用户、任务计数与最近行、计划、审计行、脱敏设置、已配置凭据状态，以及 Host 的受支持操作集合。凭据检查调用 `embymedia-admin.credentialCheck`，且只渲染 Host 返回的可用性、消息与延迟。

描述写入的按钮会创建中文 canonical 操作计划提示词，并把它排入当前选中会话。成功交接会关闭工作台，并让运营方返回 AI 对话进行计划审阅与审批。没有选中会话、binding 不可用、Remote 失败或提示词被拒绝都会显示为错误；UI 绝不会退回直接变更。

-----

<a id="understand-the-implementation"></a>
## 理解实现

<details>
<summary>实现细节——点击展开</summary>

Host 包入口只为了让 Loader 选择此包；浏览器注册位于 `src/client/index.ts`。一个 slot contribution 把稳定工具名映射到 `OperationCard`，另一个把 `EmbyWorkspace` 安装为 shell overlay。两个界面都接收适配 Remote 响应与当前会话 binding 的窄 callback，而不是导入 Host 实现代码。

| 文件 | 职责 |
|---|---|
| [`src/index.ts`](src/index.ts) | 刻意为空的 Host 插件入口 |
| [`src/client/index.ts`](src/client/index.ts) | 客户端注入、卡片 slot 注册、Remote 适配器与工作台注册 |
| [`src/client/OperationCard.tsx`](src/client/OperationCard.tsx) | 持久调用／结果解析、卡片渲染、fallback 视图与凭据暂存输入 |
| [`src/client/EmbyWorkspace.tsx`](src/client/EmbyWorkspace.tsx) | 快照工作台、凭据检查与提示词交接 |
| [`src/tools.ts`](src/tools.ts) | 与 invariant 共用的稳定十三工具卡片 roster |
| [`src/invariant.ts`](src/invariant.ts) | 卡片 roster 唯一性与数量检查 |

</details>

-----

<a id="further-exploration"></a>
## 进一步探索

当展示行为不足以回答问题时阅读以下页面。它们从共享工具 UI 进入提供数据的 Host 操作与 preset。

- [客户端工具 UI](../../client/ui-tool/README.zh.md)——共享工具视图 props 与 slot 归属。
- [EmbyMedia 操作](../operations/README.zh.md)——Host Remote、canonical 计划与验证语义。
- [EmbyMedia preset](../preset/README.zh.md)——接收工作台计划提示词的运营 persona。
- [Cordis 入门](../../../docs/cordis-primer.zh.md)——Host 与 Client 插件组合。

-----

<a id="model-experience"></a>
## 模型体验

### 仅浏览器展示

#### 模型看到什么

本包不会向模型输入添加任何内容。`EMBYMEDIA_CARD_TOOLS` 为已经记录的调用选择浏览器 renderer，工作台按钮则通过现有会话控制器提交普通用户提示词。

#### Token 影响

直接 token 为零。只有用户选择操作且会话控制器把工作台提示词接受为用户消息后，该提示词才会消耗 token；此后它是普通对话输入。

#### KV Cache 影响

卡片渲染、快照刷新、凭据检查与 overlay 状态不会改变请求前缀。排队的工作台提示词会追加到对话中，并且只影响后续请求后缀。

## 已知限制与延期工作

<a id="known-limitations-and-deferred-work"></a>

这些限制说明本包的浏览器与 Remote 依赖。

- **仅有浏览器行为**——Host 入口不执行注册；headless 组合不会从本包得到卡片或工作台。
- **Remote 可用性是必需条件**——在 `embymedia-admin` 就绪前，工作台无法加载或检查凭据；在 `embymedia-secrets` 就绪前，卡片凭据暂存无法成功。
- **快照刷新采用轮询**——工作台打开时立即刷新，并在打开期间每 30 秒刷新；两次轮询之间不订阅推送更新。
- **写入需要选中的对话**——工作台操作只排队提示词；没有当前可用会话时会明确失败，且绝不直接执行。
- **卡片要求 JSON 对象 payload**——格式错误、非对象或协议漂移的结果使用原始 fallback 视图，并失去结构化 label。
- **凭据输入刻意保持狭窄**——只有普通 `config.credential_rotate` 计划会得到输入框；webhook-secret 生成与其他所有 secret 流程仍由 Host 拥有。

<a id="dev-note"></a>
### 开发备注

<details>
<summary>维护者的工作上下文——点击展开</summary>

None.

</details>
