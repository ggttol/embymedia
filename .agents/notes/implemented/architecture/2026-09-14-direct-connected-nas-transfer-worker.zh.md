# Agent Note: 直连 NAS 传输 Worker

Status: implemented

[English](2026-09-14-direct-connected-nas-transfer-worker.md) | 中文

## Problem

生产 Debian 主机可以访问部分夸克 CDN 节点池，却对另一些节点静默丢包：同一秒内，一个签名下载地址族在 TCP 建连阶段失败，而另一个节点池可以完成。因此这个单一所有者服务器拥有了一条自己无法可靠访问的数据面，而把批量字节交给 HTTP 或 SOCKS 代理，等于用计费隧道来规避路由故障。

夸克到 115 导入还把下载字节与数据库所有者耦合在一起：中转文件、分段检查点以及向 115 发布数据的 CloudDrive2 挂载都位于该主机。CDN 区域被阻断会让无关的媒体工作停滞，操作者也无法把传输指向连通性正常的机器。

## Decision

受限命令 SSH Worker 只把夸克到 115 的数据面移到可直连的 NAS。Debian 仍然是唯一的生产数据库与任务所有者：它选择资源、验证 provider 身份、持久保存任务状态、取消工作，并执行最终的 115 验证。每次操作只有一个有界 JSON 请求经过加密 stdin，Worker 是一次性进程，在输出逐行事件后退出。

`internal/transfer` 拥有共享的下载、检查点与发布机制。下载保持四条范围连接与 10 MiB 分段上限，且分段检查点仅在其字节持久化后才写入，因此重启会复用已完成的工作而不是重新下载。发布直接写入最终目标名称，因为在 CloudDrive2 仍在上传时重命名 FUSE 文件会让 115 永久保留一个 `<name>**..uploading` 对象，且内容身份改为通过 115 API 确认，而不是回读文件。Worker 的 HTTP transport 绝不读取环境代理。

NAS 运行独立的 CloudDrive2 实例，拥有自己的 `/Config`、挂载根、缓存与管理端口，以及私有中转区和固定的 `_待整理/.embymedia-nas-worker-health-canary`。Debian 在接受导入前通过 115 API 解析该 canary，从而把 NAS 挂载与控制端绑定到已配置的 115 账号；不一致会在任何 provider 写入前失败。只有在 Debian 验证准确的 115 父目录、名称、大小与 SHA-1 并发送独立 commit 请求后，才会删除后台中转字节。

## Alternatives considered

**只发布夸克专用代理路由。** 不予采用，因为它仍要为每个传输字节花费计费隧道，并且让数据库所有者继续承担数据面。

**在 NAS 前再套一层 OpenList、WebDAV 或其它挂载。** 不予采用，因为额外组件不会改变 CDN 路径，而现有分段下载已符合实测的夸克行为。

**让两台主机共享一套 CloudDrive2 实例、挂载或中转区。** 不予采用，因为独立的 FUSE 挂载、缓存与管理端口能让任一主机的恢复不影响另一台，而两个写入方发布同一目标名称将无法验证。

**让 Worker 自行判定完成。** 不予采用，因为只有控制端持有 115 凭据与任务记录；Worker 自报成功无法证明目标对象的身份。

## Consequences

控制端不再受 CDN 区域阻断影响，因为数据面不再依赖它的出口。控制端安装保持原子性，NAS 安装保持幂等：安装器只替换一个 `authorized_keys` 条目，而不带任何命令参数、经由唯一免密 sudo 规则可达的归 root 所有助手，只能对已配置挂载下一个已存在目录执行 chown 与 chmod，并使用 `O_DIRECTORY | O_NOFOLLOW` 解析路径。

仍有两项运维耦合。传输依赖 NAS CloudDrive2 挂载与 canary，因此 NAS 侧故障会让导入明确失败并保留可恢复证据，而不是回退到代理。夸克凭据只在下载操作中经过加密 stdin，因此不会出现在进程参数、NAS 配置、进度输出与日志中。
