# Agent Note: EmbyMedia 部署 Caddy 时不记录环境变量且不重启进程

Status: implemented

[English](2026-09-08-embymedia-caddy-reload-hardening.md) | 中文

## Problem

Debian 的 Caddy 软件包使用 `caddy run --environ` 启动服务。一个 systemd drop-in 还会载入已废弃的 DSH 上游 Cookie，导致进程每次启动都把该凭据复制到 journald。完整重启 Caddy 随后会等待进程停止超时；即使 EmbyMedia 发布只修改配置，主机上的全部路由也会短暂中断。

## Decision

EmbyMedia 将 `/etc/systemd/system/caddy.service.d/embymedia-login.conf` 作为受回滚保护的部署配置安装。该 drop-in 清空继承的 `EnvironmentFile` 指令，并将 `ExecStart` 替换为 `caddy run --config /etc/caddy/Caddyfile`，因此 Caddy 不会向 journald 枚举环境变量，也不会收到已废弃的 DSH Cookie。

安装程序在新 Caddyfile 和 systemd drop-in 通过验证后 reload Caddy。回滚会还原两个文件，重新载入 systemd，并 reload 正在运行的 Caddy 进程。只有进程维护或软件包维护才重启 Caddy。

## Alternatives considered

**保留 `--environ`，只删除旧 Cookie 文件。** 这能删除已发现的凭据，但以后向服务添加任何变量时，环境枚举仍会再次产生泄露风险。

**每次发布后重启 Caddy。** 重启可以应用配置，但会中断无关的 Emby、CloudDrive 和资源路由，还可能阻塞至停止超时。Caddy 的 reload 路径无需替换进程即可应用已验证配置。

**直接管理发行版提供的软件包 unit。** 软件包升级可能替换该文件。项目自有 drop-in 保留软件包所有权，同时明确安全敏感的启动命令。

## Consequences

常规 EmbyMedia 发布不会暴露服务环境变量，也不会中断 Caddy 共享路由。部署回滚必须同时跟踪 drop-in 和 Caddyfile；格式错误的替换会在 reload 前失败，reload 失败则还原两个文件并保持上一发布版本可用。升级 Caddy 二进制或更改进程级设置时，运维人员必须另行重启 Caddy。
