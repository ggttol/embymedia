---
description: "面向需要 canonical 预览、审批、执行与独立验证的部署，说明确定性的 Emby、115、STRM、元数据、用户与清理操作。"
kind: "package-reference"
---

# @embymedia/dsh-operations

[English](README.md) | 中文

## 概述

`dsh-operations` 让 EmbyMedia 运营方查询实时媒体库事实，并通过 canonical 计划、审批、执行与验证流程完成受支持的写入。它区分新媒体库根目录入库（`resource.add_new`）与既有 Series 的缺集更新（`series.update`），并通过 `media.delete` 和 `dedup.delete` 支持显式的媒体与重复项删除。写入策略默认快速失败，而 staging 模式把计划限制到配置的 canonical 媒体库或 115 目录 ID。仅当组合提供所需 Host 服务、PostgreSQL 状态、上游凭据与 EmbyMedia 专用部署路径时才选择它。

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

在已经提供 `webServer`、`agents`、`credentials` 与 `typert` 的组合中挂载 Host 服务；在应接收 EmbyMedia 工具的 agent 组合中挂载本包的 `/tools` 入口。

### 何时选择

当写入必须把确定性预览绑定到一个会话与主体、按风险请求审批、在执行前立即重新验证目标，并在报告完成前独立重读事实时，选择此包。通用 Emby 客户端，或缺少本包 PostgreSQL schema、凭据服务、115 布局、STRM 根目录与产品 Remote 消费方的安装不应选择它。

### 最小配置

Host 配置项选择保存 PostgreSQL 连接字符串的环境变量。服务初始化时，所选变量必须非空；外围组合负责提供被注入的服务。

```yaml
- name: '@embymedia/dsh-operations'
  config:
    databaseUrlEnv: EMBYMEDIA_DATABASE_URL
```

| 字段 | 默认值 | 含义 |
|---|---|---|
| `databaseUrlEnv` | `EMBYMEDIA_DATABASE_URL` | 保存 PostgreSQL 连接字符串的环境变量 |
| `writeMode` | `disabled` | `disabled` 拒绝所有计划；`staging` 把目标限制到 allowlist；`enabled` 允许 canonical 目标 |
| `stagingLibraryIds` | `[]` | staging 模式接受的 canonical Emby 媒体库 ID |
| `stagingCids` | `[]` | staging 模式接受的 canonical 115 目录 ID |
| `schedulerEnabled` | `false` | 当写入也启用时，部署是否启动计划工作 |
| `allowInsecureResourceApi` | `false` | 显式允许携带凭据的非 loopback Resource API 使用 HTTP；除非部署已接受明文传输，否则保持禁用 |
| `approvalMode` | `manual` | `manual` 为非低风险写入请求审批；`safe-auto` 仅对源码拥有的安全列表跳过审批 |

[`src/config.ts`](src/config.ts) 是路径、端点、并发、主体选择、环境覆盖、校验与其他所有可接受字段的穷尽式真源。部署专用值与凭据不属于本 README。

### 读写语义

读取工具为健康、媒体库、资源、Series 状态、分析、任务、审计、用户、计划与配置返回有界结构化事实。`list_items` 在服务端执行一次规范化标题查询，忽略标点、引号、符号、空白、全半角与大小写差异，因此模型无需探测名称变体。查询工具描述会点明每个 action 所需的标识，并引导模型使用所属查询。`embymedia_plan` 从校验使用的同一 specification 推导每种操作接受的字段列表。[`src/tools/index.ts`](src/tools/index.ts) 拥有工具 schema 与 action 名单。

写入从 `embymedia_plan` 开始。Host 验证精确输入，捕获显式目标与验证事实，应用 `writeMode`，保存 canonical confirmation 与 hash，并把计划绑定到调用 Session 和配置的 principal。`embymedia_execute` 只接受 `planId`；Host 重新加载已保存的 hash 与 confirmation，在审批前后立即重新验证，在审批请求中标明 preview hash 和目标 ID，执行受支持的 handler，并在返回前运行独立验证。读取终态验证结果仍会校验 Session 和 principal 所有权。外部效果发生后的取消或失败会产生持久化 partial 或 verifying 状态，不会以掩盖效果的终态取消结束。

`safe-auto` 仅适用于本包有界的非破坏性集合。`resource.add_new`、媒体库扫描、海报应用或修复以及元数据刷新可以在确定性计划后跳过重复审批。`series.update`、`media.delete`、`dedup.delete` 与其他所有高风险或严重风险操作仍会请求审批。

### 新资源与既有 Series

`resource.add_new` 可以从一个可验证的 115 分享根或 1–100 个逐集分享候选创建新的媒体库条目。批量输入使用 `candidates:[{candidateId}]` 和一个通过 `outputFolder` 指定 Series 文件夹的 canonical scan 对象。Host 会解析 targetCid，暂存每个提取码，验证顶层名称唯一，转存全部分享，把所有 STRM 写入该 Series 文件夹，等待每个媒体叶可见，只刷新一次 Emby，并验证全部预期集数。一个批次只创建一个 plan、执行一次并扫描一次。

既有 Series 的缺集使用 `series.update`。一个 candidate 可以证明多个缺集，也可以通过 `candidates:[{candidateId}]` 在一个 plan 中合并 1–100 个独立单集分享。Host 只联合受支持视频叶的集数键，拒绝重复路径和未覆盖的 requested episode，解析既有 Series CID，把全部分享转存到该目录，等待每个请求叶文件可见，只扫描一次既有 Series，并验证全部请求缺集消失且 Series ID、路径与 TMDB 身份不变。

`gaps_summary`、`status` 与 `workbench` 每页最多计算 100 个 Series，并报告是否还有后续 Series。`gaps_summary` 返回不完整 Series 与显式 blocked row，并省略路径与逐集数组，因此运营方不会把 TMDB／Emby 认证、限流、取消或上游失败误认为零缺集。`resource_plan` 最多选择 20 个缺集，并返回 numbering mode、`gapTotal`、`deferredGapCount` 与 `searchHasMore`；运营方必须继续后续 Series 页与候选页，不能根据一个响应报告整个媒体库或 Series 已完成。

资源搜索与 `resource.stage_share` 都会为 115 分享生成不透明且绑定 Session 的 `candidateId`。`resource.stage_share` 接受一条用户提供的标题和分享链接，但不返回其 URL 或提取码。`accessCodeStaged=true` 表示 Host 已为预览与执行保存该提取码；它不是认证 blocker，运营 Agent 也不会要求用户再次输入。公开文本会移除控制符、遮蔽 URL／凭据并限制长度与数组数量。标记为 `diskType=115` 的结果只有在 URL 能解析为官方 `115.com` 或 `115cdn.com` 分享时才会公开。`resource.inspect_candidate` 接受同一 Session 的 ID，最多返回 500 条脱敏递归 evidence，并附带紧凑集数覆盖计数、`evidenceTotal` 与 `truncated`。`resource.list_entries` 分页返回直接子项事实；`resource.snapshot_share` 分页返回未保护分享的浅层根。

已完结 Series 在没有逐集候选时，只能把单根 Series 整包用作替换根，不能把它当作原地更新。入库前的 snapshot 必须证明一个可转存根且没有冲突 TMDB 标记，但不需要枚举每一集。`resource.add_new` 创建一个临时的独立根；扫描后的 Emby 事实必须识别新的相同 TMDB Series、证明 `missingCount=0`、确认旧 Series 仍在，并拒绝歧义。之后才能通过单独审批的 `dedup.delete` 保留新的完整 Series，并删除全部旧的不完整 Series。入库失败或内容不完整时，旧根保持不变。

`media.delete` 可以删除显式条目和完整选中的媒体根。Movie、Video 或 Series 只有在请求选中一个直接根下全部 Emby 媒体条目时才能共用该根；plan 只删除一次该根。执行器会在源仍存在时先请求 Emby 删除每个记录，再删除 STRM 与 115 存储。如果 Emby 拒绝删除条目，执行器仍会删除经过审批的确切存储根，刷新 Emby 媒体库，并等待源已不存在的记录消失，再判定成功或 partial。`dedup.delete` 继续作为完整当前相同 TMDB 分组中保留一个 keeper 的操作。严重风险清理只需一次审批；partial 清理只能重试同一组目标。

-----

<a id="understand-the-implementation"></a>
## 理解实现

<details>
<summary>实现细节——点击展开</summary>

服务从预览到验证始终保留一条持久操作记录。领域服务生成确定性预览；规划器存储其 canonical 投影；执行器检查归属、过期、策略、审批与目标新鲜度；验证器记录独立观察到的事实与最终终态。服务为浏览器 UI 暴露 Host Remote，而单独的 `/tools` 入口把同一运行时适配为模型可见工具。

Host 还在 loopback listener 上暴露 `POST /internal/embymedia/tool`，供受信的同主机适配器使用。该路由接收与 `/tools` 相同的封闭 wire-tool 名称与参数，把请求绑定到调用方给出的单一 session ID，并返回相同的结构化结果。Caddy 拒绝 `/internal/*`，非 loopback 对端 fail closed；适配器自动给出一次性批准，而规划、目标重验证、审计与验证仍是权威流程。

| 文件 | 职责 |
|---|---|
| [`src/service.ts`](src/service.ts) | 服务生命周期、Host Remote、工具分派、健康与受支持操作投影 |
| [`src/operations.ts`](src/operations.ts) | Canonical 规划、风险元数据、精确输入校验与写入策略 |
| [`src/execution.ts`](src/execution.ts) | 会话绑定执行、审批、目标重验证与审计状态转换 |
| [`src/verification.ts`](src/verification.ts) | 独立验证与终态记录 |
| [`src/operation-runtime.ts`](src/operation-runtime.ts) | 当前具体操作的预览、处理器与验证器接线 |
| [`src/tools/index.ts`](src/tools/index.ts) | Agent 工具注册与结构化结果渲染 |
| [`src/invariant.ts`](src/invariant.ts) | 包拥有名单的稳定 ID 唯一性检查 |

</details>

-----

<a id="further-exploration"></a>
## 进一步探索

当包级行为不足以回答问题时阅读以下页面。它们从 Cordis 组合进入打包的运营 preset 与浏览器展示。

- [Cordis 入门](../../../docs/cordis-primer.zh.md)——插件配置项、注入与组合行为。
- [EmbyMedia preset](../preset/README.zh.md)——消费这些工具的运营 persona 与八个操作 skill。
- [EmbyMedia 客户端 UI](../ui/README.zh.md)——工具卡片、凭据暂存与 Remote 支撑的工作台。
- [`src/capabilities.ts`](src/capabilities.ts)——稳定包词汇与操作 ID。

-----

<a id="model-experience"></a>
## 模型体验

### EmbyMedia 工具与结构化结果

#### 模型看到什么

挂载 `/tools` 入口后，模型会收到本包有界的 `embymedia_*` 查询工具，以及 `embymedia_plan`、`embymedia_execute` 与 `embymedia_verify`。工具结果是当前事实、canonical 计划、执行状态与验证状态的 JSON 投影；本包不添加系统提示词章节。

#### Token 影响

入口启用时，固定工具 schema 消耗请求 token。每次调用添加一个数据相关的 JSON 工具结果；结果大小遵循所选查询上限或有界显式计划目标，而不是无界目录转储。

#### KV Cache 影响

工具 schema 前缀在一个 preset 组合的生命周期内稳定。工具调用与结果追加在此前缀之后，因此只影响对话后缀，而不会重写更早请求内容；改变挂载的 preset 会为新会话建立不同前缀。

## 已知限制与延期工作

<a id="known-limitations-and-deferred-work"></a>

这些约束说明本包何时不可用或何时需要新计划。

- **部署专用基础设施**——启动需要 PostgreSQL 连接与 Host 服务依赖；具体操作还会在缺少对应 Emby、115、TMDB、资源 API、文件系统或凭据前提时失败。
- **只公布完整操作三件套**——只有预览、执行与验证全部接线的 kind 才会报告为受支持；已声明但不完整的 kind 无法通过运行时规划。
- **分享访问码位于进程内**——资源搜索与 `resource.stage_share` 只暴露绑定 Session 的 candidate ID；candidate 或 plan 过期以及服务重启都会丢弃该值，因此尚未执行的受保护分享更新必须暂存新的 candidate。持久化 confirmation 与最终验证不会恢复或暴露访问码。
- **新入库只接受窄分享形式**——`resource.add_new` 要求一个经过包装且非空的 115 分享根目录，并且只支持 `keep` 旧版本策略；磁力／离线入库需要单独工作流。
- **写入与计划工作选择性启用**——`writeMode` 默认为 `disabled`，且只有两个部署设置都允许工作时 scheduler 才会启用。

<a id="dev-note"></a>
### 开发备注

<details>
<summary>维护者的工作上下文——点击展开</summary>

None.

</details>
