# EmbyMedia 测试

[English](testing.md) | 中文

## 概要

无需 provider 凭据即可验证 Go 服务、浏览器构建与部署工具。真实媒体验收是单独且需要明确授权的操作。

## 目录

- [仓库检查](#仓库检查)
- [行为与生产证据](#行为与生产证据)

## 仓库检查

运行 `make install-web` 后，使用 `make test` 执行 Go 竞态测试与 Python 部署测试，使用 `make check` 执行 Vue 类型检查、Go vet 与文档检查。`make build` 验证内嵌浏览器及服务 artifact；`make build-linux` 准备 Linux 部署 artifact 及校验和。这些目标负责准备 web 资源，因此干净检出不依赖已提交的构建输出。

CI 覆盖 Go 竞态测试、Vue 类型检查及构建、部署 Python 测试与 Linux artifact。它不需要 DeepSeek key、外部模型服务、生产数据库或媒体 provider 凭据。本地检查成功不能证明 Emby、CloudDrive2 或 115 可用。

## 行为与生产证据

Go 测试位于所属包旁。[`deploy/scripts`](../deploy/scripts) 下的部署测试通过隔离 fixture 覆盖登录、快照、release 行为、媒体规范化及媒体库切换。应让合理的失败保持可见：授权拒绝、身份漂移、工作中断、挂载失效与 provider 副作用部分完成。

修复缺陷时，只保留能因合理缺陷而失败的回归测试。不要固定偶然文案、源码文本、mock 转发值或实现布局。浏览器变更需要在真实运行的 Vue 应用中使用可丢弃数据验证；仅编译不能证明键盘、布局或工作流行为。

绝不能把破坏性真实媒体操作作为自动验证捷径。读取 provider 结果与持久任务终态；发现工具不等于执行，接受后台工作不等于完成。生产备份与隔离还原是恢复证据，不是覆盖运行服务的授权。[运维指南](operations.zh.md)与[安全说明](../SAFETY.zh.md)定义这些限制。
