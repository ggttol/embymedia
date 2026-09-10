# 参与 EmbyMedia 开发

[English](CONTRIBUTING.md) | 中文

## 概要

参与 Go 服务、Vue 应用或部署工具开发，同时保留生产数据与媒体操作安全保障。

## 目录

- [工作流](#工作流)
- [审查要求](#审查要求)

## 工作流

遵循[开发指南](docs/development.zh.md)。使用 `make install-web` 安装冻结前端依赖，使用 `make build` 构建，并在提交变更前运行 `make test` 与 `make check`。保持变更范围明确，只描述实际执行的验证。

使用可丢弃数据库与 provider fixture。源码变更不授权部署、数据库删除、凭据修改或真实媒体修改。[安全说明](SAFETY.zh.md)与[运维指南](docs/operations.zh.md)定义这些限制。

## 审查要求

保留共享身份验证、任务持久性、源身份检查、STRM ACL 及备份／还原兼容性。同步更新受影响的中英文文档，并在 [Agent Notes](.agents/notes/README.zh.md) 中记录重要决策。不要修改冻结归档记录。

UI 变更应包含真实浏览器证据，缺陷修复应包含合理回归验证。不要新增只断言文案或实现布局的测试。移动或删除源码时保留现有[许可证](LICENSE)及适用第三方归属声明。
