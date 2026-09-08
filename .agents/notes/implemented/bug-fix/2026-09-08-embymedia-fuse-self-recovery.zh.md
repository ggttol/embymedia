# Agent Note: EmbyMedia 自动恢复失效的 CloudDrive FUSE 挂载

Status: implemented

[English](2026-09-08-embymedia-fuse-self-recovery.md) | 中文

## Problem

CloudDrive2 的用户态 FUSE 会话停止响应后，进程仍可能保持运行。内核保留挂载记录，因此 `findmnt` 成功，而 `stat` 返回 `State not recoverable`。Docker 会把容器标记为不健康，但不会重启不健康的容器。防火墙重新加载还使用了 `flush ruleset`，它会删除 Docker 的 NAT 链，并可能阻止修复后的容器启动。

## Decision

Compose 服务栈负责确定性的 FUSE 清理。服务栈启动前删除已有但不可读的挂载，服务栈停止全部容器后惰性卸载残留的 CloudDrive 挂载。人工维护和普通服务重启因此使用同一条清理路径。

`embymedia-clouddrive-recovery.timer` 每分钟检查 CloudDrive 容器和挂载健康标记。容器明确处于不健康状态时立即恢复；其他故障需要连续观察三次。恢复与部署和备份共用锁，在执行有副作用的工作前停止 V2，先停止 Emby 再停止 CloudDrive，删除失效挂载，启动 CloudDrive 并验证宿主机健康标记，启动 Emby 并验证其 `/media` 视图，最后在成功或失败时恢复 V2。恢复操作每十五分钟最多启动一次，因此上游故障不会形成重启循环。

nftables 文件只替换 `inet embymedia_filter`，绝不清空完整规则集，因此 Docker 会保留用于实现容器端口发布的链。

## Alternatives considered

**依赖 Docker 健康检查和 `restart: unless-stopped`。** Docker 健康状态只用于观测；重启策略响应进程退出，不响应 `unhealthy`。故障进程因此可能无限期保留失效的 FUSE 挂载。

**恢复时重启 Docker 守护进程。** 重启 Docker 会重建缺失的 NAT 链，但也会中断宿主机上的所有容器。挂载恢复只停止 CloudDrive、Emby 和 V2；保留 Docker 的 nftables 表后，不再需要重启守护进程。

**启用宿主机全局透明代理或文件系统干预。** 网络路由不能恢复已经失效的 FUSE 用户态会话，而宿主机全局拦截会把故障范围扩大到媒体服务栈之外。

## Consequences

失效挂载会变成有界的服务中断，而非持续的人工运维事件。恢复会主动中断 V2 任务并重启 Emby，因为继续使用不可读或已替换的绑定挂载不安全；被中断的有副作用任务仍保持失败，等待显式复核。连续三次观测会过滤瞬时文件系统延迟；冷却期用最多十五分钟的额外恢复延迟换取上游故障期间不会重复重启。
