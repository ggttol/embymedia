# Agent Note: canonical Series 绑定定义缺集补齐完成

Status: implemented

[English](2026-08-31-canonical-series-missing-episode-binding.md) | 中文

## Problem

缺集修复扩展既有 Emby Series，并不是新根目录入库。115 条目已转存、STRM 路径已生成或 Emby 路径可见，只能证明存储与扫描已经推进，不能证明 Emby 已把每个 requested episode 归入目标 Series。第二个媒体库根目录即使可见，原 Series 仍可能保留缺集，Emby 也可能暴露具有相同 TMDB 身份的重复 Series。

115 访问码是用于检查和转存受保护分享的临时 secret。该 secret 离开 plan 和 Session 后，完成状态仍必须可验证。因此，持久的 Series、TMDB、集数及放置事实定义最终结果。

## Decision

缺集工作从 `series.gaps_summary` 开始；其有界清单只返回不完整 Series，不包含路径或逐集 payload。随后再对一个 canonical `libraryId` 与 `seriesId` 调用 `series.resource_plan`。resource plan 返回 canonical Series、编号模式、搜索查询、精确的已播出 `requestedEpisodes`，以及已把受保护分享凭据替换为不透明且绑定 Session 的 candidate ID 的搜索结果。`resource.stage_share` 会从一条用户提供的 115 分享与显示标题创建同一种 candidate，且不返回分享 URL 或访问码。创建计划前，`resource.inspect_candidate` 会为一个同 Session ID 创建 snapshot，并返回不含分享 URL 或访问码的有界脱敏顶层根与递归 evidence。候选发现只搜索 115 资源，先使用 Series 名称加集数键；没有候选时仅使用 Series 名称重试一次，返回的查询标识向模型展示的结果集。写入输入严格为 `{libraryId,seriesId,numberingMode?,candidate,requestedEpisodes:[{season,episode,absolute?}]}`，并使用 `series.update`。`resource.add_new` 只创建新的媒体库根条目，绝不修复既有 Series 的缺集。

已完结 Series 如果没有任何候选的 115 snapshot 能够证明覆盖全部 requested gap，就使用两个 plan 的根替换流程，而不是降低原地 `series.update` 的证据要求。替换候选的 snapshot 必须证明一个可转存的 Series 根且没有冲突 TMDB 标记，但不需要枚举每一集。`resource.add_new` 创建一个临时的独立媒体库根；扫描后的 Emby 事实定义完整性。它们必须识别出一个 `missingCount=0` 的新相同 TMDB Series、确认每个旧 Series 仍然存在，并无歧义地给出完整的相同 TMDB 分组。随后，单独审批的 `dedup.delete` 把完整的新 Series 指定为 keeper，把每个旧的不完整 Series 指定为删除目标。入库、TMDB 绑定、集数完整性或重复分组身份验证失败时，旧根保持不变。正在更新的 Series 绝不使用该替换路径。

Host 解析媒体库名称、作为直接子项的 Series 文件夹与路径、Emby TMDB 身份、115 媒体库根 CID，以及唯一的既有 115 Series CID。有界递归 115 分享 snapshot 只保留选定顶层 ID 作为转存单元，并把后代 ID、名称、路径、类型与大小哈希为证据。存在后代时，只有受支持的视频叶文件名能够证明请求的季集或绝对集数；聚合分享标题、根目录范围与父目录范围只作为身份提示，不能声称树中不存在的文件。没有后代的扁平分享只能使用选定且受支持的视频根文件名。深度超过 20 或后代条目超过 10,000 会在转存前失败。模型提供的显示标题可以拒绝冲突 TMDB 标记，但不能证明内容。匹配缺失、有歧义、冲突、不完整或超限都会在转存前失败。

执行把资源保存到 canonical 115 目标，并等待 plan 绑定的每个媒体叶都在 CloudDrive 中可见。新 Series 批量入库与既有 Series 批量补集都接受 1–100 个 opaque candidate，绑定每个 snapshot 与已暂存提取码，转存全部分享并只扫描一次。既有 Series 补集还会联合叶文件集数键，拒绝重复路径和未覆盖的 requested gap，并确保 canonical Series ID、路径、TMDB 身份、编号模式与目标 CID 在预览、审批、执行和验证期间保持不变。

最终验证使用 plan 中非 secret 的 canonical 事实和当前 Emby 状态。它要求流水线验证与扫描完成，目标 Series 保持可读且 ID、路径及 TMDB 身份不变，每个请求的季集或绝对集数都从该 Series 的已播缺集中消失，并且该 Series 是媒体库中具有此 TMDB 身份的唯一 Series。验证不会重新打开受保护分享，也不需要其 115 访问码。

任务中心仅为名称完全匹配的 `电视剧追更`、`综艺追更` Emby 媒体库及其同名 115 CID 映射提供 `series_auto_fill`，并沿用该规则。每次执行从 Emby 读取已播出且有编号的缺集，拒绝没有唯一 TMDB 身份或没有唯一 115 直接子目录的 Series，为每个 Series 检索配置范围内的 1–30 个有效 115 候选，并在深度 20、最多 10,000 个条目的限制内递归检查分享；只有包含明确且匹配集数标记的受支持视频叶文件可以入选。转存模式通常把这些叶文件 ID 接收到既有 Series CID，等待 CloudDrive，同步 STRM，扫描 Emby 并验证原 Series。同时启用 `replace_completed_pack` 和浏览器拥有的危险操作开关后，只有视频叶文件覆盖全部既有与缺失已播集并集的独立候选根才可以替换旧根。任务先把新根转存到旧根旁，等待完整集数集合可见，扫描并要求唯一的新相同 TMDB Series 不含已播缺集且拥有全部预期集数，然后删除旧 Emby 条目、回收准确的旧 115 CID、移除受限于 STRM 根内的旧目录，再次扫描并报告替换。删除前的任何失败都会保留旧根。预检模式只执行发现，不产生 provider 写入。

候选标签与每个入选视频自身的祖先目录都必须独立符合 canonical Series 身份；匹配的搜索词不能覆盖另一部剧的名称或 TMDB 标记。检查失败仍会使任务失败并保留逐 Series 结果，与已经检查但没有合适集数的分享区分。完结整包尝试失败时，仅移除经核验且属于该次操作的暂存数据；切换状态不确定时要求恢复处理，不删除任何未经验证的副本。

共享 Drive 服务持有一个可取消的快照请求执行名额，直至响应解码和关闭完成。可配置的 `share_snapshot_interval_ms` 默认在上一响应结束后等待 1000 毫秒，覆盖分页、递归目录、候选以及并发调用方，不叠加候选级等待。类型化 HTTP 405/429 错误会停止整次补集执行，包括完结整包检查和后续 Series；成功 HTTP 响应中仅包含类似状态码的文本不会触发停止。不完整快照保留已完成页面的证据，但不能据此授权转存。Provider 是否接受请求仍取决于外部条件，不能由请求间隔保证。

已批准的清理会绑定确切的 Emby 条目、路径／类型／TMDB 事实、有界递归 STRM 与 115 manifest，以及确切的 115 parent／ID／名称／类型事实。`media.delete` 可以删除显式条目和完整选中的媒体根。共用根下的全部 Emby 媒体条目都必须被选中；不拥有根的条目成为仅删除 Emby 的目标，由最后一个 owner 只删除一次 STRM 与 115 根。执行会在删除存储前请求 Emby 删除记录。当 Emby 拒绝删除条目时，执行仍会删除经过审批的确切存储，刷新媒体库，并等待源已不存在的记录消失。`dedup.delete` 继续作为独立的保留一个 keeper 操作。作用后的取消、审计失败、组件失败或事实验证失败都会成为 partial，且绑定 Session owner 的 retry 只恢复同一组目标。

Emby 原生管理员删除使用经过认证的原生设备会话和每个条目的 `CanDelete` 权限，不接受代理身份、Agent token 或应用 API key。确认框同时列出生成的 STRM 路径与原视频路径。网关串行化媒体修改，预检原生 `DeleteInfo` 返回的确切路径，仅在原生删除成功后回收经过身份复核的 115 原文件。回收失败会保留为有审计记录的部分完成结果，不回滚，也不自动重放。默认文件系统 ACL 保留 Emby 对生成目录的共享写权限，不放宽服务 umask。

## Testing

聚焦的 Series 领域测试固定 TMDB 标记、canonical 路径、季集与绝对集数范围、分页状态、显式 numbering mode、类型化 blocker、递归分享证据、误导性目录与整包标题拒绝，以及有界 `resource_plan` 输出。Host action 测试调用同 Session 的候选检查和分页 115 直接子项检查，证明不包含 secret 与 URL、目录标记经过脱敏、evidence 受限以及覆盖计数完整且紧凑。操作运行时与服务测试固定 snapshot hash、占用根拒绝、CID 唯一性、无需暂存访问码的受保护分享验证、审批后重新验证、作用后取消记账，以及 Series／TMDB／episode 完成条件。清理、路径、客户端、mutation、执行与验证测试固定效果前的完整集合预检、精确 Emby／STRM／115 绑定、有界流式 manifest、显式 keeper、绑定目标的审批文本、终态所有权检查、可恢复验证，以及绑定 owner 的 partial 重试。

独立任务测试通过与 Emby、资源索引和 115 协议兼容的 fixture 执行真实队列路径，覆盖缺集发现、资源检索、递归 115 检查、精确接收、CloudDrive 可见性、STRM 创建、Emby 扫描完成和扫描后缺集验证。替换测试要求完整集数覆盖和危险操作开关，在删除旧 Emby 条目、旧 115 CID 和受限 STRM 根之前验证新 Series，并固定该删除顺序。单独的测试会拒绝其他媒体库和重复 TMDB Series，排除未来或无编号条目，并防止把分辨率等数字误识别为集数键。

独立删除测试覆盖原生管理员与设备授权、应用 key 拒绝、原路径确认、源身份变化、原生删除失败和部分回收。真实 Emby 原生删除使用隔离的两集临时媒体，验证单集删除不影响相邻集，再验证 Series 删除及原视频回收。

## Alternatives considered

**把路径可见视为完成。** 不采用，因为可见性只能确定存储发现，不能确定集数归属。第二个根目录或 Emby 未绑定到目标 Series requested episode 的文件也可能满足这一条件。

**把 `resource.add_new` 直接视为缺集完成。** 不采用，因为 add-new 拥有新的媒体库根条目并验证根可见性，而不是既有 Series ID、缺集集合或 Series CID。已完结 Series 的替换路径只把 add-new 用于临时根；只有当前相同 TMDB 事实证明新根完整，且单独的 `dedup.delete` 验证每个旧的不完整根均已不存在后，才报告完成。

**只根据标题或文件夹字符串解析目标。** 不采用，因为名称可能变化或冲突。解析过程把 canonical Emby 文件夹及 TMDB 身份与唯一性检查结合，存在歧义时快速失败。

**在最终验证时重新打开 115 分享。** 不采用，因为访问码是临时 secret，分享可用性也不是后置条件。当前 Emby Series 绑定和 requested episode 覆盖情况无需保留或重放凭据即可观测。

**只按名称删除可疑根目录或重复 Series。** 不采用，因为名称不能证明直接根目录关系，也不能标识完整重复集合。清理会绑定确切的 Emby、STRM 和 115 snapshot，而重复删除要求显式 keeper 和完整的相同 TMDB 分组。

## Consequences

缺集成功表示目标既有 Series 在其 canonical TMDB 与存储根目录下拥有 requested episodes，或已验证完整的新 Series 在删除旧根后成为唯一的相同 TMDB keeper。canonical 绑定、资源证据、目标唯一性、替换完整性或重复分组身份不确定时，修复都会快速失败。自动整包替换同时要求显式任务选项和浏览器拥有的危险操作开关；这会增加无人值守删除风险，但暂存的新版本拥有每个预期已播集之前绝不会删除旧根。最终验证无需恢复 secret 即可重复执行，同时普通新标题入库继续使用独立的 `resource.add_new` 约定。
