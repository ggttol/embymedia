# EmbyMedia 运维

[English](operations.md) | 中文

## 概要

运维独立 Debian 媒体服务时，保留数据库、浏览器身份、provider 凭据、媒体与恢复流程。安装和恢复需要运维人员明确授权；源码清理不授予此权限。

## 目录

- [部署](#部署)
- [NAS 传输 Worker](#nas-传输-worker)
- [访问](#访问)
- [备份与隔离还原](#备份与隔离还原)
- [媒体与挂载安全](#媒体与挂载安全)

## 部署

支持的 release 布局是 `/opt/embymedia-v2/current/bin/embymedia`，部署文件位于同一 release 根目录下。`embymedia-v2.service` 拥有 `/srv/embymedia/data/embymedia.db` 并监听回环 HTTP 端口 3080。Caddy 与 HTTP 登录服务保护公开访问；Compose 管理 Emby 和 CloudDrive2。主机准备与镜像配置由 [`deploy/scripts`](../deploy/scripts) 和 [`deploy/compose.yml`](../deploy/compose.yml) 定义。

首次 secret 初始化在主机准备完成后以 root 运行，参数为 artifact 目录；其中的 `manifest.json` 必须包含 `imageIds.clouddrive2` 与 `imageIds.emby`。提供 Docker 可用的不可变 `repository@sha256:<digest>` 镜像引用；`init-secrets.sh` 读取该清单但不拉取镜像。已有 V2 的升级使用现有 stack 配置，无须重新执行首次 secret 初始化。

备份并检查 release 后，在源码检出中运行 `make install-web` 与 `make build-linux`。将 release 源码以及 `bin/` 中两个 Linux 二进制与各自的 `.sha256` 文件传至已准备好的 Debian 主机。在该 release 源码目录中，授权运维人员可以使用唯一 release ID 安装：

```sh
sudo deploy/scripts/install-release.sh "$PWD" RELEASE_ID
```

将 `RELEASE_ID` 替换为尚不存在的新 release 名称。安装器校验 artifact、检查数据库并原子切换 `current`。它在停止 V2 前最多等待一小时让运行中的后台工作结束；等待超时会保持当前 release 可用。`EMBYMEDIA_DEPLOY_FORCE=1` 改为取消该工作，仅适用于明确接受中断的情况。等待中的任务持久保留；中断的有副作用尝试必须先检查再重试。

release 安装保留 webhook 启用选择、浏览器登录、生成的 STRM 根目录与回滚跟踪配置。共享的 `/etc/caddy/Caddyfile` 及其他应用的路由片段由主机管理；EmbyMedia release 只更新 `/etc/caddy/snippets/embymedia.caddy`、`/etc/caddy/routes/embymedia.caddy` 与 `/etc/caddy/sites/embymedia.caddy`，验证完整配置后再 reload Caddy。systemd override 阻止日志枚举环境变量。artifact 构建、源码清理与 CI 均不执行部署。

夸克到 115 导入先保存到所选夸克目录，再逐文件写入中转区，并在默认 115 账号的固定 `/emby/_待整理` 目录完成验证上传。`transfer_temp_dir` 默认为 `/srv/embymedia/data/transfers`；`transfer_min_free_bytes` 默认为 10 GiB，且可用空间还必须容纳当前文件。`quark_download_connections` 设置单文件的并行范围连接数（默认 4，允许 1-8）；实测 4 条约等于单条流的 3.4 倍，而 8 条低于 4 条。配置 115 开放平台 token 时使用原生秒传或分片上传；没有 token 时，将 `clouddrive_c115_account_id` 设为对应托管账号，且可写 CloudDrive2 挂载必须映射 `/115open/emby`。导入器先写任务拥有的暂存文件，`fsync` 后发布，再通过 115 核对最终父目录、名称、大小与 SHA-1，确认后才删除中转文件。CloudDrive2 FUSE 挂载把目录报告为 root 所有，且不会传播默认 ACL，因此 `ensure-transfer-access.sh` 为服务账号授予 `_待整理`、`电视剧追更` 与 `综艺追更` 的访问权限；`embymedia-v2` 单元在启动前执行它，CloudDrive 监控每分钟重跑一次，使之后创建的目录无需重启即可写入。普通导入直接平铺发布到 `_待整理`，不再镜像夸克分享自身的目录名，因为通过 provider API 创建的子目录对服务永不可写。中转区应位于持久且私有的存储上，使中断工作可在重启后协调恢复。

只更新两集时，在夸克分享弹窗读取递归预览，仅勾选这两个文件，提交前核对已选数量和字节数。REST 先调用 `POST /api/v1/drive/share-snapshot` 并传 `recursive:true`，再调用 `POST /api/v1/quark/share-imports` 并传 `selected_source_manifest:[{id,revision,name,size},...]`；MCP 先用 `quark_snapshot_share`，再用 `quark_import_share_to_115`。空选择会报错，不会整包导入。整包下载必须明确设置 `import_all:true`；未明确同意整包的旧客户端及旧持久任务将被拒绝。不要通过移动文件到另一个夸克目录或修改生产数据库绕过此限制。

用指定链接自动补缺集时，提交 `series_auto_fill`，参数为 `{"libraries":["电视剧追更"],"series_ids":["EMBY_SERIES_ID"],"source_shares":{"EMBY_SERIES_ID":{"provider":"quark","url":"https://pan.quark.cn/s/SHARE_CODE"}},"transfer":true}`；预检使用 `transfer:false`。`candidate_overrides` 接受资源索引 ID，不是分享码；`/api/v1/search` 使用 `q`，并拒绝不支持的 `keyword` 参数。匹配仍要求可信剧集身份和已播缺集；提供 URL 不等于授权替换已有剧集。选择失败或有歧义时停止，不会退回整包。

传输必须走持久 Worker 流程。把 CDN 响应直接流式写入最终 FUSE 媒体文件名会暴露未完成视频，不能凭文件存在或大小近似就生成 STRM。入库前应通过 115 核实准确大小与 SHA-1。Debian 和 NAS Worker 均支持 HTTP 412 凭据恢复；NAS 修复生效需要同时部署对应 Worker 二进制和服务端。预检失败且不存在导入行时可以重试；已有导入行但没有协调好的保存根时，需要检查，不能自动重新转存。

## NAS 传输 Worker

可选的 NAS Worker 保持 Debian 是唯一生产数据库与任务所有者，同时把夸克下载字节及由 CloudDrive2 支撑的 115 写入移到可直连的 NAS。每个实例使用独立的 CloudDrive2 `/Config`、挂载根、缓存与管理端口；NAS 挂载把同一托管 115 账号的 `/115open/emby` 目录映射到 `/CloudNAS/CloudDrive`。Worker 要求共享的 `.embymedia-health-canary`、可由 CloudDrive2 与 115 API 同时看到且身份准确的 `_待整理/.embymedia-nas-worker-health-canary`、可写目标目录、持久私有中转区，并保留配置的最低空闲空间及当前文件大小。它绝不会回退到 HTTP 或 SOCKS 代理。

在 Debian 上提供预先验证的 OpenSSH `known_hosts` 条目，不要在部署时信任网络扫描结果。控制端配置会创建由 `embymedia` 拥有的专用 Ed25519 密钥，记录严格主机密钥检查，并把四个 `EMBYMEDIA_NAS_WORKER_SSH_*` 变量加入 `/etc/embymedia/v2.env`：

```sh
sudo deploy/scripts/configure-nas-worker-controller.sh gaotao@NAS_HOST 5022 VERIFIED_KNOWN_HOSTS
```

向 NAS 传输 Worker 二进制、校验和、部署文件与生成的公钥，不传输任何私有凭据。先由获授权的 NAS 管理员一次性安装归 root 所有且限制路径的访问助手，再以受限 NAS 账号运行 Worker 安装器，随后启动固定 digest 的 CloudDrive2 Compose 栈，并通过回环地址或受保护局域网 Web UI 配置其 115 挂载：

```sh
sudo deploy/scripts/install-nas-worker-root.sh RELEASE_TREE /volume1/docker/clouddrive2/CloudNAS/CloudDrive gaotao 1026 100
deploy/scripts/install-nas-worker.sh RELEASE_TREE CONTROLLER_PUBLIC_KEY
CLOUDDRIVE_IMAGE=repository@sha256:digest docker compose -f /volume1/homes/gaotao/embymedia-transfer/clouddrive-compose.yml up -d --wait
```

安装器只替换 `~/.ssh/authorized_keys` 中的 `embymedia-nas-worker` 条目；该密钥不能分配 PTY、转发端口、使用 agent 或执行任意命令。它唯一的提权能力是一个不带命令参数的准确免密助手：Worker 通过 stdin 传入已验证的相对目标，归 root 所有的助手把所有权修改限制在已配置挂载下一个已存在目录。夸克凭据只在下载操作中经过加密 stdin，不进入参数、NAS 配置、进度输出或日志。传输在 NAS 保存范围检查点与中转文件，发布前计算完整文件 hash，通过 NAS CloudDrive2 挂载直接写入最终目标名称，并等待 Debian 验证准确的 115 父目录、名称、大小与 SHA-1，之后才由独立 commit 请求删除中转文件。绝不要重命名仍在上传的 CloudDrive2 文件：115 会留下一个永远不会结算的 `<name>**..uploading` 对象。当存在此类占位对象且没有已结算对象时，Worker 会重写目标，因为挂载仍会把这个陈旧文件报告为完整大小。SSH 丢失、取消、挂载失败、已有外来同名目标或 provider 状态有歧义都会明确失败并保留可恢复证据。

每个手动提交的夸克导入在 115 身份全部核对后，控制端都会确定性地创建唯一一个 `media_ingest` 接力任务，不需要额外自动计划。只有当编号视频及相关字幕通过标题加年份或 TMDB 证据唯一证明一个现有 Emby Series 时，接力任务才移动这些文件；不支持的文件与有歧义的匹配继续保留在 `_待整理`。随后它同步对应媒体库 STRM，等待 Emby 扫描，并验证已导入且已播出的集号。任务中心把两个持久任务归为一条七阶段流程。重试传输不会重复创建接力；应在合并流程中重试当前失败阶段。

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
