# Agent Note: EmbyMedia 备份使用在线一致性快照

Status: implemented

[English](2026-09-08-embymedia-online-backups.md) | 中文

## Problem

文件级 Restic 读取无法让活动 SQLite 的主文件、WAL 和 SHM 文件保持事务一致。停止完整 Compose 服务栈可以让这些文件静止，但会中断播放、断开 CloudDrive 的 FUSE 进程，并使每日备份变成重复的可用性和挂载恢复事件。

## Decision

备份进程持有既有部署锁，并在 `/srv/embymedia/backups` 下构建私有树。V2 和所有容器保持运行。`snapshot-live.py` 在该树下保留每个选定的绝对路径；普通文件读取前后的设备、inode、大小和修改时间保持稳定时才会被接受，发生变化的文件最多重试三次。Emby cache、日志和转码暂存内容，以及 CloudDrive 日志、临时文件和更新均为派生或易变数据，不进入恢复快照。

带 SQLite 头的文件通过 SQLite 在线备份 API 复制，并以 `PRAGMA quick_check` 验证。暂存树省略对应的 WAL、SHM 和 rollback-journal companion，因为复制后的数据库已经包含一个提交快照。文件 mode、owner、时间戳、目录和 symlink 都会保留。每次暂存都会创建新文件，因此 Restic 忽略暂存 inode 和 ctime；保留的大小与修改时间使未变化文件可以复用父快照。可写 Restic cache 使索引工作不会进入服务受保护的 home 目录。格式标记在 Restic 记录暂存布局前标识该布局，而退出清理会删除完整或不完整的暂存树。

隔离恢复同时接受历史直接路径快照和在线暂存格式。它会先把暂存的规范路径具体化，再验证浏览器 identity 并运行独立数据库检查。因此，生产恢复保持相同的最终文件系统布局，同时无需让 Restic 读取变化中的数据库。

## Alternatives considered

**继续停止 Compose 服务栈。** 该方案可以获得简单的静止文件系统，但会不必要地中断独立服务，并每天主动拆除 FUSE owner。

**在 writer 运行时一起复制 SQLite 数据库、WAL 和 SHM 文件。** 各文件复制发生在不同时刻；文件名集合相符并不能建立同一个已提交数据库状态。

**直接备份活动路径并接受 Restic 退出码 3。** 不完整快照不是恢复机制。重试稳定普通文件并使用 SQLite 备份 API，会在快照发布前让不一致明确失败。

**通过 Restic stdin 存储单个 tar 流。** 流可以保留规范归档路径，但会放弃 Restic 文件级浏览，并使隔离验证依赖额外的归档解压格式。

## Consequences

每日备份不再造成应用或媒体停机。临时快照在 Restic 完成前最多消耗约等于所选数据量的空间；普通文件不共享一个全局事务时间点，但可变数据库共享。连续三次复制都发生变化的文件会使本次任务失败，而不会替换有效 Restic 快照。恢复代码增加一条明确的暂存布局规范化路径，同时保留历史快照支持。
