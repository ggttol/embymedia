# Agent Notes

[English](README.md) | 中文

Agent Notes 记录当前 EmbyMedia 决策、理由、被否决的替代方案与后果。代码及用户指南负责当前行为与操作流程；记录应保留决策价值，而非重复实现清单。

## 布局与格式

活跃记录使用 `{lifecycle}/{kind}/yyyy-mm-dd-topic.md`，并带中文与 `.i18n.yaml` 旁车。生命周期为 `proposed`、`implemented`、`rejected`；分类为 `architecture`、`feature`、`bug-fix`、`simplification`、`process`、`testing`。文件名日期是最初提议日期。不要创建集中记录索引。

每种语言以 `# Agent Note: <title>`、空行和 `Status: proposed`、`Status: implemented` 或 `Status: rejected — <reason>` 开头。机器可读前缀与状态保持英文。每条记录的正文以 `## Problem` 开始并记录 `## Alternatives considered`。已实现记录使用 `## Decision` 与 `## Consequences`；提案使用 `## Proposal`、`## Acceptance criteria` 与 `## Risks`。

重要行为、架构、共享契约、工具、测试策略或持久格式变更需要更新或新增记录。保持已实现事实当前有效，不在原记录中反转决策。反转需要新的决策所有者，说明替代内容与仍受保护的内容。遵循[文档配对规则](../../docs/AGENTS.md)。

## 保留与替代

仍能指导安全、所有权、持久格式、替代方案或重新引入条件的已实现理由保持活跃。当完成的决策不再约束独立应用时，归档整个已实现三件套。字数与年龄不是保留标准。

否决过时提案，不要归档提案。当已否决记录的前提消失，且理由无法阻止合理误区时，删除整个三件套。只有当前所有者吸收所有仍有用的理由、替代方案、后果与验证缺口后，才能删除完全被替代的已实现记录。部分替代保持相互链接。

## 冻结历史

[`archived/`](archived/AGENTS.md) 保存历史快照，而非当前指令。现有归档语言文件、旁车与 manifest 条目不可变。绝不修复其出站链接，也不将其中已移除 runtime 的假设用于 V2。

新的归档操作将整个三件套从 `implemented/<kind>` 移至 `archived/<kind>`，在两种语言状态行正下方插入相同的 `Archived: YYYY-MM-DD`，机械更新配对 hash，并向 [`archived/manifest.json`](archived/manifest.json) 追加原始字节 SHA-256 封印。不要修改归档正文。同一变更内修复活跃文档的入站链接。

manifest 包含 `version: 1` 与 `files` 映射，后者将归档相对文件名映射到 `sha256:<hex>`。保留每个现有 key 与 hash。`make check` 通过 Python 文档检查器验证归档完整性与活跃配对，无需已移除的 npm workspace 或生成器。
