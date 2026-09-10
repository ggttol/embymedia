# EmbyMedia 运维

[English](operations.md) | 中文

## 概要

运维独立 Debian 媒体服务时，保留数据库、浏览器身份、provider 凭据、媒体与恢复流程。安装和恢复需要运维人员明确授权；源码清理不授予此权限。

## 目录

- [部署](#部署)
- [访问](#访问)
- [备份与隔离还原](#备份与隔离还原)
- [媒体与挂载安全](#媒体与挂载安全)

## 部署

支持的 release 布局是 `/opt/embymedia-v2/current/bin/embymedia`，部署文件位于同一 release 根目录下。`embymedia-v2.service` 拥有 `/srv/embymedia/data/embymedia.db` 并监听回环 HTTP 端口 3080。Caddy 与 HTTP 登录服务保护公开访问；Compose 管理 Emby 和 CloudDrive2。主机准备与镜像配置由 [`deploy/scripts`](../deploy/scripts) 和 [`deploy/compose.yml`](../deploy/compose.yml) 定义。

首次 secret 初始化在主机准备完成后以 root 运行，参数为 artifact 目录；其中的 `manifest.json` 必须包含 `imageIds.clouddrive2` 与 `imageIds.emby`。提供 Docker 可用的不可变 `repository@sha256:<digest>` 镜像引用；`init-secrets.sh` 读取该清单但不拉取镜像。已有 V2 的升级使用现有 stack 配置，无须重新执行首次 secret 初始化。

备份并检查 release 后，在源码检出中运行 `make install-web` 与 `make build-linux`。将 release 源码、`bin/embymedia-linux-amd64` 及 `bin/embymedia-linux-amd64.sha256` 传至已准备好的 Debian 主机。在该 release 源码目录中，授权运维人员可以使用唯一 release ID 安装：

```sh
sudo deploy/scripts/install-release.sh "$PWD" RELEASE_ID
```

将 `RELEASE_ID` 替换为尚不存在的新 release 名称。安装器校验 artifact、检查数据库并原子切换 `current`。它在停止 V2 前最多等待一小时让运行中的后台工作结束；等待超时会保持当前 release 可用。`EMBYMEDIA_DEPLOY_FORCE=1` 改为取消该工作，仅适用于明确接受中断的情况。等待中的任务持久保留；中断的有副作用尝试必须先检查再重试。

release 安装保留 webhook 启用选择、浏览器登录、生成的 STRM 根目录与回滚跟踪配置。Caddy 配置变更使用验证后的 reload，而非例行重启；systemd override 阻止日志枚举环境变量。artifact 构建、源码清理与 CI 均不执行部署。

## 访问

在 `/agent` 创建分别命名的 Agent token，安全保管仅返回一次的明文 secret。主机上的客户端携带 `Authorization: Bearer <token>` 或 `X-Agent-Token: <token>` 连接 `http://127.0.0.1:3080/mcp`。远程访问使用经过身份验证的部署端点与适当受保护的网络路径。明文 HTTP 不会加密传输中的密码或 token。

Caddy 在公开 Agent 路由上删除调用方提供的浏览器身份 header。浏览器登录与 Agent scope 是不同权限来源；Agent token 不能批准删除请求或启用危险操作开关。Emby 原生删除要求用户拥有获授权的 Emby 管理员设备会话。详见[安全说明](../SAFETY.zh.md)。

共享生产状态的客户端应使用运行中的 HTTP 端点。绝不能对活跃数据库另启 stdio 或数据库检查进程：独占锁防止任务恢复、迁移及自动计划相互竞争。隔离的 `-check-db` 可以迁移其副本，并不是只读数据库检查工具。

## 备份与隔离还原

已安装备份任务保持 V2 和 Compose 栈运行。它通过在线备份 API 暂存 SQLite 数据库，使用 `PRAGMA quick_check` 检查，重试持续变化的普通文件，并排除已复制数据库的 WAL/SHM 伴随文件。Restic 接收私有暂存树，其中包含 V2 数据库、浏览器用户数据库、Emby 与 CloudDrive 配置，以及生成的 `/srv/embymedia/data/strm-v2` 目录。

授权运维人员可以运行已安装备份，并将指定快照还原至全新隔离目录：

```sh
sudo /opt/embymedia-v2/current/deploy/scripts/backup.sh
sudo /opt/embymedia-v2/current/deploy/scripts/restore-isolated.sh SNAPSHOT_ID /srv/embymedia/restore-DRILL_ID
```

替换两个 ID；还原目录必须尚不存在。还原支持直接路径与暂存快照，验证恢复的浏览器身份，确保生成的 STRM 目录存在，并调用当前二进制检查隔离数据库。它不会激活还原树。应保留未经修改的快照，因为验证可能迁移还原数据库。

不要手工复制运行中的 SQLite 文件，也不要用未经验证的还原覆盖生产状态。Restic 恢复密码与 provider 凭据应安全存储在源码仓库之外；所列备份输入不包含 `/etc/embymedia/secrets` 或原始媒体。保留策略保存十四份每日快照，因此本地备份成功不等于具备异机灾难恢复保障。

## 媒体与挂载安全

Emby 对生成 STRM 目录的共享读写访问依赖 [`configure-strm-access.sh`](../deploy/scripts/configure-strm-access.sh) 中的 ACL 策略。安装、还原与创建目录时都要保留它。不要扩大原始媒体权限来替代对生成目录访问问题的修复。

CloudDrive 恢复 timer 观察容器健康与挂载 canary，再持有部署／备份锁执行有界恢复，并设十五分钟冷却期。恢复可能中断活跃媒体任务；事后检查任务与 provider 状态。挂载表中存在条目不代表 FUSE 文件系统可用。

规范化与媒体库切换脚本是需明确调用的维护工具，不是启动钩子。冻结清单、ID 绑定计划、暂存检查／回收根目录及替代媒体库检查可避免删除未经验证的副本。不要仅为验证源码检出而运行这些工具。[规范化决策](../.agents/notes/implemented/feature/2026-09-09-canonical-media-library-normalization.zh.md)记录其取舍。
