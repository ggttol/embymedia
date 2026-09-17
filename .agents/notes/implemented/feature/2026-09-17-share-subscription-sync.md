# 网盘分享订阅主动追更方案 (Share Subscription Sync)

## 背景
当前 EmbyMedia 依赖 Emby 的 `/Shows/Missing`（结合定时刷新）以检测缺集，并触发基于网盘关键字搜索的转存任务 (`series_auto_fill`)。
然而，由于 Emby 内置针对 TMDB 请求的节流（缓存过期前不发起有效请求），加之 TMDB 首播日期往往不精准（时区或时间点问题），自动补集常会有 12~24 小时的延迟。
为了彻底解决时效性，通过直接订阅并监控网盘分享链接来感知最新集数，完成转存并触发 Emby Inotify，反向激活入库，以达到第一时间看剧的需求。

## 目标
新增一套分享订阅自动同步（`share_auto_sync`）机制，与现有 `series_auto_fill` （作为长效兜底补漏）并存。

## 核心实体
在 `internal/domain/models.go` 定义实体 `ShareSubscription`，并在 SQLite 数据库建立对应表：
- `id`: UUID (Primary Key)
- `name`: 订阅名称，例如 "交锋 - Quark追更"
- `provider`: "quark", "115", 等
- `url`: 监控的分享链接
- `password`: 分享密码（如果有）
- `target_cid`: 115 目的地 CID
- `active`: 布尔值，是否启用
- `last_cursor_time`: 最新已同步资源的创建时间（用于增量防重）
- `last_sync_at`: 最新一次轮询时间
- `error`: 最近的错误信息

## 后端实现细节
1. **Repository层 (`internal/db`)**
   为 `ShareSubscription` 编写一套基本的 CRUD SQL操作 (`CreateShareSubscription`, `ListShareSubscriptions`, `UpdateShareSubscriptionCursor` 等)。
2. **API层 (`internal/api`)**
   注册路由：
   - `GET /api/v1/share-subscriptions`
   - `POST /api/v1/share-subscriptions`
   - `PUT /api/v1/share-subscriptions/:id`
   - `DELETE /api/v1/share-subscriptions/:id`
3. **任务机制 (`internal/service/taskqueue.go` & `share_subscription.go`)**
   - 增加任务类型 `"share_auto_sync"`。
   - `runShareAutoSync`:
     1. 从 `db` 取出处于 active 状态的 subscriptions。
     2. 根据 Provider 去调用对应 Share 解析，拉取文件列表（平铺资源记录，包含文件创建时间、大小等）。
     3. 过滤条件：`file.CreatedAt > subscription.LastCursorTime`。
     4. 将增量文件封装，下发明细到对应执行单元：
        - 如果是 115 的同盘分享，则使用 `SaveShareCtx`。
        - 如果是 夸克提取，构建一个受控的 `quark_to_115_import` (类似现已有的跨盘转存)，通过指定的 file id 过滤。
     5. 只要至少成功处理了一项，更新订阅的 `LastCursorTime` 从而完成增量闭环。

## 调度
通过类似于现有的 `series_auto_fill` 定时（如每 30 分钟）扫面并排队 `share_auto_sync` 任务。老的 `series_auto_fill` 则继续按照原架构以小时级频次跑，两者互不干涉。
