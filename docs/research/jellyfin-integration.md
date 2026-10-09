# Jellyfin 集成面调研（短剧媒体库 + 下载后自动刷新）

- 调研对象：guo（Go 后端）将产出的"无加密视频 + NFO + 图片 + ASS 弹幕字幕"媒体库，目标是 Jellyfin（Emby 兼容格式）可直接导入，并可在下载完成后通过 Jellyfin API 触发库刷新。
- 本地对照素材（只读）：`/home/zhao/clone/guoapp/lib/media_library.dart`（`embyShowNfo`、`embyEpisodeNfo`、`_safeName`、`exportJobs`）、`/home/zhao/clone/guoapp/lib/media_exports.dart`（`embySpecialNfo`、`_showDirectory`、`_writeShowMetadata`、`exportMerged`）。
- 外部结论来源分级：**[官方文档]** jellyfin.org/docs；**[官方源码]** github.com/jellyfin/jellyfin（master、v10.11.0、v10.10.7、v10.10.0 四个版本交叉核对）；**[guoapp]** 本地文件/函数名；**[社区]** 论坛、第三方项目（仅作辅助，均已标注）。
- 方法说明：对本报告两条决策级结论（库刷新 API 权限与参数、NFO 内相对图片路径失效）尝试过独立 source_check 交叉核验，但该 provider 两次均未能提取到段落（missing-evidence）；已按纪律改为直接抓取原始源码与官方 issue 全文核对（issue #10010 中用户引用的正是同一处源码逻辑，与本报告源码阅读一致）。此为已披露的核验限制。
- 版本口径：Jellyfin 当前稳定版为 10.11.x（2025 年底发布）。本文所有 API 结论均以 v10.10.7 / v10.11.0 / master 源码为准，并对社区流传的旧参数（`Recursive`、`X-Emby-Token`、`?api_key=`）标注过时风险。

---

## 1. 短剧内容在 Jellyfin 中的库结构最佳实践

**结论目录布局（[官方文档] TV Shows 页）：**

```
Shows/
└── 剧名/                        # 一剧一目录；可加 (年份) 与 [provider id]
    ├── tvshow.nfo
    ├── poster.jpg
    ├── Season 01/               # 季目录必须写全 "Season"，补零对齐；不可缩写为 S01/SE01
    │   ├── S01E001.mkv
    │   ├── S01E001.nfo
    │   ├── S01E001.zh.ass
    │   └── S01E001-thumb.jpg    # 可选：分集剧照
    └── Season 00/               # 特典（specials）
        ├── S00E001.mkv
        └── S00E001.nfo
```

要点（均出自 https://jellyfin.org/docs/general/server/media/shows ）：

1. **层级**：剧目录 → 季目录 → 分集文件；"Do not mix Season folders with episodes in the Shows folder"（不要把分集直接散落在库根/剧根与季目录混放）。
2. **季目录命名**：`Season *`，数字补零保证位数一致（`Season 5` → `Season 05`）；官方明确不要缩写成 `S01`/`SE01`。
3. **剧目录命名**：`Series Name (year) [metadata provider id]`，年份与 provider id 均为可选，provider id 应是已知提供方标识（文档示例 `[imdbid-tt00000000]`）。红果短剧基本不在 TMDb/TVDb 覆盖范围内，**建议只用剧名（可加年份）**，避免伪造 provider id 引发错误匹配。
4. **分集命名**：`SxxEyy` 惯例；一个文件含多集（`S01E01-E02`）会被显示成单一条目，官方建议拆分——短剧逐集导出天然满足。
5. **保留字符**：文件名禁用 `< > : " / \ | ? *`，否则"will cause problems"。
6. **特典**：放入 `Season 00`，编号 `S00Exx`。官方建议：当元数据提供方无法识别该特典时，"recommended to use a name which describes the content of the special instead of naming it `Series Name S00Exy.mkv`"（用描述性文件名防止错误抓取元数据）。若同时提供分集 NFO，本地 NFO 优先（见第 2 节），错配风险已很低。
7. 特典若要"显示在正季内"，需要服务器开启 `Dashboard -> Library -> Display` 的对应选项，并在 NFO 中写 `airsbefore_season` / `airsbefore_episode` / `airsafter_season`（见第 2 节 guoapp 对照）。

**与 guoapp 对照：**

- `exportJobs`（media_library.dart）：`exports/<剧名> [hongguo-<hash12>]/Season 01/S01E001.mkv` —— 层级、`Season 01`、`S01E001` 全部符合官方规范；分集号补足 3 位（`E001`）没问题：官方 Kodi 标准解析正则为 `[Ss](?<seasonnumber>[0-9]+)[][ ._-]*[Ee](?<epnumber>[0-9]+)`，`[0-9]+` 不限位数（[官方源码] `Emby.Naming/Common/NamingOptions.cs` EpisodeExpressions）。
- `exportMerged`（media_exports.dart）：特典写 `Season 00/S00E001.mkv` —— 符合官方特典目录规范。
- `_safeName`（media_library.dart）：已剔除 `< > : " / \ | ? *` 与控制字符并截断 60 字符 —— 与官方保留字符清单完全对齐。
- **需斟酌**：`_showDirectory` 的剧目录后缀 `[hongguo-<hash12>]`。官方只背书方括号内放 provider id；该后缀非标准。[官方源码] `NamingOptions.CleanStrings` 含剥离 `\\[...\\]` 后缀的清理规则（`^\s*(?<cleaned>.+?)((\s*\[[^\]]+\]\s*)+)(\.[^\s]+)?$`），因此标题清理时会被剥掉、不参与搜索匹配（源码推断，未实测）；其防同名剧冲突的工程价值可以保留，但不要在方括号里放会误导匹配的 ID 形态字符串。

---

## 2. tvshow.nfo 与分集 NFO 的字段规范

**官方依据：** [官方文档] Local .nfo metadata（https://jellyfin.org/docs/general/server/metadata/nfo ）+ [官方源码] `MediaBrowser.XbmcMetadata/Parsers/`（`BaseNfoParser.cs`、`SeriesNfoParser.cs`、`EpisodeNfoParser.cs`，master）。

**文件命名（官方文档表）：**

| 媒体 | NFO 文件名 |
|---|---|
| 剧集 | `tvshow.nfo` |
| 季 | `season.nfo`（可选） |
| 分集 | `<分集文件名>.nfo`（与视频同基名） |

两条关键全局行为（官方文档原文）：

- "It's currently not possible to disable .nfo metadata. **Local metadata will always be fetched and has priority over remote metadata providers like TMDb.**" —— 对红果短剧（TMDb 无条目）极其有利：NFO 是权威来源。
- "If there are multiple tags that map to the same internal Jellyfin data like `plot` and `review`, **the last of these tags in the file will have priority**."

**tvshow.nfo 可用字段（文档通用标签表 + SeriesNfoParser/BaseNfoParser 源码 case）：** `title`/`name`/`localtitle`（→Name）、`plot`/`biography`/`review`（→Overview）、`year`（>1850 才生效）、`premiered`/`aired`/`releasedate`（→PremiereDate，按服务器 NFO 日期格式解析）、`genre`（可多个，`/` 分隔）、`studio`、`tag`、`status`、`rating`/`ratings`、`uniqueid`（`type` 属性指定提供方）、`thumb`（见下方修正）、`fanart`（子节点 `thumb` → Backdrop）、`actor`、`lockdata`（锁定元数据防在线覆盖）等。

**分集 NFO 字段（EpisodeNfoParser 源码 case，逐一验证）：** `season`→ParentIndexNumber、`episode`→IndexNumber、`episodenumberend`、`showtitle`→SeriesName、`airsbefore_episode` **或** `displayepisode`→AirsBeforeEpisodeNumber、`airsbefore_season` **或** `displayseason`→AirsBeforeSeasonNumber、`airsafter_season` **或** `displayafterseason`→AirsAfterSeasonNumber；其余走 BaseNfoParser 通用字段（title/plot/aired/uniqueid/…）。多 `<episodedetails>` 块会被按集号排序合并（一文件多集场景）。

**与 guoapp 逐条对照：**

| guoapp 现状（函数/字段） | Jellyfin 行为 | 结论 |
|---|---|---|
| `embyShowNfo`：`<title>`+`<plot>` | BaseNfoParser：title→Name、plot→Overview | ✅ 直接沿用 |
| `embyShowNfo`：`<uniqueid type="zhenguojian" default="true">` | `uniqueid` 只读 `type` 属性；未知 type 原样保存为自定义 provider id；**`default` 属性被忽略（无害）** | ✅ 沿用；`default` 属性可留（Kodi/Emby 习惯）可删 |
| `embyShowNfo`：`<thumb aspect="poster">poster.jpg</thumb>`（本地封面时） | **需修正**：`FetchThumbNode` 要求值能通过 `Uri.TryCreate(val, UriKind.Absolute, ...)`，相对文件名 `poster.jpg` 直接失败，仅记日志忽略（[官方源码] BaseNfoParser.cs；用户在 issue #10010 中引用同一代码证实相对路径不被支持，该 issue 以 NOT_PLANNED/失效关闭，即至今未支持） | ❌ 删除该行即可：`poster.jpg` 放在剧目录会被本地图片规则自动识别为 Primary（第 3 节），NFO 里根本不需要再指一次 |
| `embyShowNfo`：封面未落地时写远程 URL 进 `<thumb>` | 绝对 http(s) URL 走 RemoteImages，有效；但每个类型只取第一个 thumb | ✅ 可用；仍建议一律落成本地 `poster.jpg` |
| `embyShowNfo`：`<season>1</season><episode>N</episode>` | SeriesNfoParser 无此 case，落入 BaseNfoParser default → `reader.Skip()`，**整体忽略**；季/集数由实际文件计算 | ⚠️ 对 Jellyfin 无效；可删（保留对 Emby 是否生效未验证） |
| `embyEpisodeNfo`：`title/showtitle/season/episode/plot/uniqueid` | 全部被解析（见上字段表） | ✅ 直接沿用；`showtitle` 会成为 SeriesName，注意与剧目录名保持一致 |
| `embyEpisodeNfo`：`<plot>` 用整剧简介 | 解析无问题，仅内容重复 | ⚠️ 建议改为分集简介（若红果接口有），提升体验 |
| `embySpecialNfo`：`<displayseason>1</displayseason><displayepisode>N+1</displayepisode>` | 被 `EpisodeNfoParser` 读为 AirsBeforeSeason=1、AirsBeforeEpisode=N+1（与 `airsbefore_*` 完全等价） | ✅ 兼容；配合服务器"Display specials within their series they aired in"选项，合并特别篇会排在 S01 第 N 集之后（即季末）。语义详见官方 shows 文档 Specials 节的排序规则 |
| `xmlText`：转义 `& < > " '` + 剔除控制字符 | XML 正确性要求 | ✅ 沿用到 Go 实现（注意 `]]>` 之类无影响，标准五实体已够） |

**给 Go 版生成器的最小字段建议：**

- `tvshow.nfo`：`title`、`plot`、`uniqueid type="hongguo" default="true"`、可选 `year`/`premiered`/`genre`/`studio`；**不含 `<thumb>`**（图片全部走文件名规则）；如担心在线提供方覆盖可加 `<lockdata>true</lockdata>`（官方文档 lockdata 字段，锁后刷新不再改写已锁字段）。
- 分集 NFO：`title`、`showtitle`、`season`、`episode`、`plot`、`uniqueid`；特典追加 `displayseason`/`displayepisode`（或等价 `airsbefore_*`）。
- 所有 NFO 与视频文件同基名同目录（`S01E001.nfo`）。

---

## 3. poster / fanart / still 图片命名与尺寸建议

**命名规则（[官方文档] shows 页 Images 节，含 Series/Season/Episode 列勾选表）：**

| 文件名（可用作独立名或后缀） | 图片类型 | 适用 |
|---|---|---|
| `poster` / `folder` / `cover` / `default` | Primary（海报） | 剧 ✅ 季 ✅ 分集 ✅ |
| `fanart` / `backdrop` / `background` / `art` | Backdrop（背景图） | 剧 ✅ 季 ✅ 分集 ✅ |
| `banner` | Banner | 剧/季 |
| `logo` / `clearlogo` | Logo | 剧/季/分集 |
| `landscape` / `thumb`（独立文件名） | Thumb | 剧/季/分集 |
| `<分集文件名>-thumb.jpg`（后缀用法） | Primary（分集剧照） | 文档原例：`S01E01 Some Episode-thumb.jpg` |

- 外置图片放在媒体文件旁边，"they will take precedence over other sources"（优先于在线来源）。
- 多张背景图可用 `backdrop1.jpg`、`backdrop-2.jpg` 追加编号。
- NFO 内引用图片必须绝对路径或 URL（见第 2 节修正项）。

**guoapp 对照：** `_writeShowMetadata`（media_exports.dart）已把封面复制为剧目录 `poster.jpg` —— 正确且足够；未产出 fanart 与分集剧照，属可选增强。Go 版可增加：`Season 01/poster.jpg`（季海报，可直接复用剧封面）、分集 `S01E001-thumb.jpg`。分集无外置图时，Jellyfin 会自动从视频截帧生成分集缩略图（**研究者推断**：基于 Jellyfin 内建 ScreenGrabber 行为的通行认知，官方 shows 文档未直接陈述，建议实测确认）。

**尺寸建议：** Jellyfin 官方文档**不规定任何像素尺寸**（jellyfin-docs issue #181 请求补充图片规格文档，官方回答是参考 fanart.tv 等刮削站惯例：https://github.com/jellyfin/jellyfin-docs/issues/181 ）。社区/上游惯例（[社区]，TMDb Image Bible）：

| 类型 | 纵横比 | 分辨率区间 | 格式 |
|---|---|---|---|
| 海报 poster | 2:3（TMDb 原文 "1:1.5 is usually preferred"） | 500×750 ~ 2000×3000 | JPEG |
| 背景 fanart/backdrop | 16:9 | 1280×720 ~ 3840×2160 | JPEG |
| 分集剧照 still | 16:9 | 1280×720 ~ 3840×2160 | JPEG |

来源：TMDb Posters（https://www.themoviedb.org/bible/image/59f7582c9251416e7100005f ）、TMDb Backdrops（https://www.themoviedb.org/bible/image/59f758339251416e71000065 ）。红果封面多为竖版，直接落 `poster.jpg` 即可，Jellyfin 不做强制裁切；不满足 2:3 也能用，仅观感问题。

---

## 4. 外挂 .ass 字幕命名规范与默认行为

**官方依据：** [官方文档] shows/movies 页 "External Subtitles and Audio Tracks" 节（https://jellyfin.org/docs/general/server/media/shows#external-subtitles-and-audio-tracks ，movies 页为同一节的重定向目标）+ [官方源码] `Emby.Naming/Common/NamingOptions.cs`。

**命名规范：**

```
Season 01/
├── S01E001.mkv
└── S01E001.zh.ass          # = 分集文件基名 + 语言/标志字段（. 分隔）+ .ass
```

- 官方文档示例即 `.ass`：`Series Name A (2021) S01E01 Title.ja.ass` —— `.ass`/`.ssa` 都在官方 `SubtitleFileExtensions` 支持列表（源码：`".ass", ".mks", ".sami", ".smi", ".srt", ".ssa", ".sub", ".sup", ".vtt"`）。
- **语言标记用 ISO 639 码**：中文用 `zh`（`chi` 亦可识别）；语言字段之后可再接标志字段，多个字段以 `.` 分隔。
- **标志字段（源码 `MediaForcedFlags`/`MediaDefaultFlags`/`MediaHearingImpairedFlags` 与文档表一致）：**
  - `default` → 默认字幕轨；
  - `forced` / `foreign` → 强制字幕；
  - `sdh` / `cc` / `hi` → 听障字幕（注意：`hi` 单独出现会被解析为印地语，需与语言码并用，如 `xxx.en.hi.srt`）；
  - 任何无法解析为语言/标志的字段会成为该字幕流的标题（如 `title.commentary.zh.aac` 的 `commentary`）。
  - 文档原文注意事项："**Flags are ignored on containers with more than one stream**"（文件名标志对多流容器不生效）。
- **弹幕建议命名**：`S01E001.zh.default.ass`（语言 zh + default 标志；是否需要 default 取决于产品是否希望"打开即显示弹幕"，两字段组合的解析顺序按官方示例"语言在前、标志在后"最稳妥，**建议实测**）。若同一分集还想保留台词字幕，可再放 `S01E001.zh[srt].srt` 之类第二轨（同名多文件会被识别为同一分集的多条字幕轨）。

**Jellyfin 默认外挂字幕行为：**

- 外挂字幕与视频同基名即可被关联（文档命名规则即关联规则）。
- 带 `default` 标志的流被标记为默认轨（源码 `MediaDefaultFlags=["default"]`）；无标志时按用户端字幕语言偏好与音频语言匹配自动选择（文档 flags 语义 + 用户偏好机制，**自动选择的完整匹配规则属源码级细节，未逐条核验**）。
- ASS 渲染：Jellyfin Web 客户端用 libass 方案渲染 ASS 样式（含弹幕定位）；转码路径由 FFmpeg 处理 ASS/SSA —— 10.11 官方发布说明明确提到 "We now have more accurate rendering of ASS/SSA subtitles when using hardware transcoding"（https://jellyfin.org/posts/jellyfin-release-10.11.0/ ），可确认服务端转码路径对 ASS 有一等支持。**不直接支持 ASS 的客户端会走转码烧录**（通行认知，**研究者推断**，未逐客户端验证）。
- 弹幕特征提示：单个 ASS 内事件数巨大（数千条）属正常，libass/ffmpeg 均可处理；但移动端客户端长时间播放的渲染性能建议实测。

---

## 5. 下载完成后触发库刷新的最小 API 面

**鉴权（决策级，务必照做）：**

- 请求头：`Authorization: MediaBrowser Token="<API_KEY>"`。该格式是官方维护者文档（https://gist.github.com/nielsvanvelzen/ea047d9028f676185832e51ffaf12a6f ，10.11 发布说明正文直接引用该文档）给定的现代方式，全版本可用。
- **不要用** `X-Emby-Token` / `X-MediaBrowser-Token` 头或 `?api_key=` 查询参数：官方 10.11.0 发布说明宣布 legacy 授权将被移除（"We're planning to remove old authorization methods in 10.12.0"，并新增了禁用 legacy 的测试开关：https://jellyfin.org/posts/jellyfin-release-10.11.0/ ）；官方 issue #16086 中维护者明确 "The X-Emby-Token header is not supported, the Authorization header is fine"（https://github.com/jellyfin/jellyfin/issues/16086 ）；生成客户端 URL 也已改为 `ApiKey` 参数（PR #13342）。社区脚本里流传的 `?api_key=`（如论坛帖）属 legacy，勿采用。
- API Key 获取：Dashboard（控制台）→ API Keys 手工创建后填入 Go 应用配置（jellyfin-web 面板路由 `apps/dashboard/routes/keys`，https://github.com/jellyfin/jellyfin-web/blob/master/src/apps/dashboard/routes/keys/index.tsx ）。API Key 具有管理员级权限（RequiresElevation 端点均可用），只应存于本地配置，绝不能进版本库。
- 口径差异记录：官方发布说明写"10.12 移除"，维护者在 seerr #2278 评论称"10.12 默认关闭、10.13 彻底移除"（https://github.com/fallenbagel/jellyseerr/issues/2278 ）。时间线口径不一致，但结论一致：**新代码只用 Authorization 头**。

**最小 API 面（推荐，两个请求即可）：**

| 请求 | 用途 | 权限 | 成功返回 |
|---|---|---|---|
| `GET /System/Info` | 配置保存时验活（URL + Key 是否有效） | 任意已认证（API Key 可） | 200 |
| `POST /Library/Refresh` | 全库扫描（发现新文件并入库） | `RequiresElevation`（API Key 满足；普通用户令牌不满足） | 204 No Content |

- [官方源码] `Jellyfin.Api/Controllers/LibraryController.cs`（master 与 v10.10.7 一致）：`[HttpPost("Library/Refresh")] [Authorize(Policy = Policies.RequiresElevation)] [ProducesResponseType(204)]`，实现为 `await _libraryManager.ValidateMediaLibrary(...)` 后返回 204 —— 即**该请求会等扫描跑完才返回**；Go 客户端要设宽松超时（分钟级）并异步触发，失败只记日志不阻断下载流程。
- 204 只代表请求被受理/完成，不代表新文件一定入库成功；必要时可配置后台轮询或仅依赖定时扫描兜底。

**按目录/定向刷新（可选进阶，注意版本语义）：**

1. `POST /Items/{itemId}/Refresh`（`[Authorize(Policy = RequiresElevation)]`，204/404）：**仅刷新该条目自身元数据，不递归发现新文件**。源码核对（v10.10.0、v10.10.7、v10.11.0、master 四版本一致）：查询参数只有 `metadataRefreshMode`、`imageRefreshMode`、`replaceAllMetadata`、`replaceAllImages`、（10.10.7+ 的）`regenerateTrickplay` —— **已无 `Recursive` 参数**。社区教程/论坛脚本仍传 `Recursive=true`（如 https://forum.jellyfin.org/t-api-endpoint-to-scan-a-single-library ），属 10.9 时代的历史遗留，多余查询参数会被 ASP.NET Core 忽略。子文件的发现逻辑在 `Folder.ValidateChildren`/库扫描管线，不在条目刷新里（[官方源码] `MediaBrowser.Providers/Manager/MetadataService.cs` 中确认条目刷新不触碰磁盘子项发现）。**结论：新下载文件入库不要指望该端点。**
2. `POST /Library/Media/Updated`，请求体 `{"Updates":[{"Path":"/abs/path/to/剧目录","UpdateType":"scan"}]}`（`[Authorize]` 默认策略即可，v10.10.7 与 master 均存在；官方 API 参考 https://api.jellyfin.org/#tag/Library/operation/PostUpdatedMedia ）：向服务器"报告这些路径发生了变化"，等效于实时监控捕获到该目录变更，社区（论坛、autopulse 等工具生态）普遍用作定向刷新。**注意**：服务器实际重扫范围是否严格限定在该路径，未能从源码核实（10.11 起工程结构变动，LibraryMonitor 实现位置未定位），如实测无显著收益，回退到 `POST /Library/Refresh` 即可。
3. `POST /Library/Series/Added?tvdbId=...`：需要 TVDB id，红果短剧不适用，排除。

**Go 端建议实现**：配置项 `jellyfin.base_url` + `jellyfin.api_key`（均可选；未配置则跳过刷新）。下载完成、所有产物（视频/NFO/图片/字幕）落盘后：异步 goroutine → `POST {base}/Library/Refresh`，带 `Authorization: MediaBrowser Token="..."` 头，超时 ≥10 分钟；401/403 记"鉴权失败"，5xx/超时记警告并支持下次重试；不向用户抛错。

---

## 附录 A：矛盾与过时信息记录

1. **legacy 鉴权移除时间线**：官方 10.11 发布说明 vs 维护者评论（10.12 移除 vs 10.12 默认关/10.13 移除）。不影响实现决策。
2. **社区教程与新版源码不符的三处**：`Recursive=true`（10.10+ 无此参数绑定）、`X-Emby-Token` 头（legacy，将移除）、`?api_key=` 查询参数（legacy，新版为 `ApiKey`）。论坛 2025 年的可用示例在 10.12+ 预期失效。
3. **tvshow.nfo 的 `<season>`/`<episode>`**：guoapp 沿用 Emby 习惯写入；Jellyfin 源码证实忽略。Emby 端是否消费未验证（不影响 Jellyfin 目标）。

## 附录 B：缺失证据 / 未决问题

- `POST /Library/Media/Updated` 触发的扫描范围是否限定于上报路径（源码未核实，仅社区用法佐证）。
- 10.9 及更早版本 `/Items/{id}/Refresh` 是否绑定 `Recursive` 参数（未验证；不影响 10.10+ 目标）。
- 无外置图时 Jellyfin 自动截帧生成分集缩略图：通行认知，未获官方文档直接陈述（研究者推断，标注于第 3 节）。
- 不支持 ASS 直渲的第三方客户端走转码烧录：通行认知（研究者推断），未逐客户端验证。
- `S01E001.zh.default.ass` 中 default 标志的实际默认选中行为、以及 guoapp `[hongguo-<hash>]` 目录后缀在真实服务器上的匹配表现：均建议 Go 版落地后用 10.11 实测。
- 红果封面原始纵横比未调研（不影响 poster.jpg 直接落地方案）。

## 附录 C：来源清单（保留）

| 来源 | 类型 | 用途 |
|---|---|---|
| TV Shows — jellyfin.org/docs/general/server/media/shows | 官方文档 | 目录/命名/特典/图片/外挂字幕标志 |
| Local .nfo metadata — jellyfin.org/docs/general/server/metadata/nfo | 官方文档 | NFO 文件名与字段、本地优先、NFO 图片路径注意 |
| MediaBrowser.XbmcMetadata/Parsers/{BaseNfoParser,SeriesNfoParser,EpisodeNfoParser}.cs（master） | 官方源码 | 字段解析逐条验证（含 thumb 绝对 URI 限制、displayseason 映射、season/episode 忽略） |
| Jellyfin.Api/Controllers/LibraryController.cs（master + v10.10.7） | 官方源码 | /Library/Refresh 权限与行为、Media/Updated、Series/Added |
| Jellyfin.Api/Controllers/ItemRefreshController.cs（v10.10.0/v10.10.7/v10.11.0/master） | 官方源码 | /Items/{id}/Refresh 参数表（无 Recursive） |
| MediaBrowser.Providers/Manager/MetadataService.cs（master） | 官方源码 | 条目刷新不做子项磁盘发现 |
| Emby.Naming/Common/NamingOptions.cs（master） | 官方源码 | SxxEyy 正则、字幕扩展名、default/forced/sdh 标志、CleanStrings 剥离方括号 |
| Jellyfin 10.11.0 发布说明 — jellyfin.org/posts/jellyfin-release-10.11.0 | 官方公告 | legacy 鉴权移除计划、ASS/SSA 硬解转码渲染 |
| nielsvanvelzen 授权文档 gist | 官方维护者 | `Authorization: MediaBrowser Token="..."` 格式 |
| jellyfin issue #10010、#16086 | 官方仓库 | NFO 相对图片路径不受支持的用户级复现；X-Emby-Token 不再支持 |
| jellyfin.org 论坛 "API endpoint to scan a single library" | 社区 | 单库/定向刷新的实践用法（含过时参数样本） |
| api.jellyfin.org（PostUpdatedMedia） | 官方 API 参考 | Media/Updated 请求体结构 |
| TMDb Image Bible（posters/backdrops）、jellyfin-docs issue #181 | 行业/官方 issue | 图片尺寸与纵横比惯例（官方无尺寸规定） |

**剔除/降权来源**：jellyfin-jellyfin.mintlify.app（非官方 API 镜像，仅线索用，结论均改由源码证实）；JellyWatch/Posterizarr 等 SEO 博客与工具 README（尺寸建议改用 TMDb 官方规范）；elest.io 快速上手（无增量信息）。

## 附录 D：后续建议（仅最有用项）

1. Go 版落地后用 Jellyfin 10.11 真机做一次端到端验收：导入含 `Season 00` 特典 + `S01E001.zh.default.ass` 的剧目录，核对 NFO 字段、poster.jpg、弹幕默认选中与特典显示位置。
2. 若全库刷新在大库上过慢，再实测 `Library/Media/Updated` 定向刷新的实际扫描范围，决定是否作为可选优化。

## 附录 E：实例级暗伤（真机实测补充，2026-10-10）

同一套媒体文件在两个 10.11 实例上表现迥异：曾经历过「12.2 启动 → 配置半清理 → 10.11 复用」的实例，分集能入库、外挂字幕能挂上，但 **IndexNumber 静默为 null、分集 NFO 静默不读**（逐条 FullRefresh、移出重导、剧名前缀、两位/三位集号均无效，且与文件内容无关——同文件在健康库解析正常）；pristine 实例上同一目录 76/76 全部正确。结论：

- 遇到「分集无集号/不吃 NFO 但字幕正常」先怀疑**实例数据库暗伤**，别急着改命名；
- 测试实例一律用 `scripts/jellyfin-test.sh reset` 重建（脚本已修好服务加载期的 503 竞态：逐步等就绪、登录验证通过才 Complete 向导）；
- 本项目产物的正确性基准：pristine 10.11 上 剧名 (年份)/Season 01/S01E001.mp4 + 同基名 .nfo + .zh.ass → 集号/标题/中文字幕全对。
