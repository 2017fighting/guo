# 红果源协议考古（基于 guoapp 参考实现）

> 调研票：`.scratch/guo-web/issues/01-hongguo-protocol-research.md`。
> 唯一依据：`/home/zhao/clone/guoapp`（只读参考，Flutter 壳 + Go 核心 `native/core`）。
> 引用约定：`文件名:函数名（行号）`，行号为调研时 guoapp 工作树的 1-based 行号，仅作定位辅助；函数名是稳定的锚点。
> 本文档只描述 guoapp 实际实现的协议面，不包含任何站源图片内容；标注「推测」的条目需要后续抓包验证。

## 0. 总览：两套入口与端点速查

guoapp 对红果有两条并行协议面：

| 面 | 基地址 | 形态 | 用途 |
|---|---|---|---|
| 官网 Web（SSR） | `https://hongguoduanju.com` | HTML 内嵌 `window._ROUTER_DATA` JSON（`provider_hongguo.go:parseRouterData:178`） | 分类兜底、搜索页、详情兜底、网页播放器取流、全部红果排行榜 |
| App API（字节系） | `https://api5-normal-sinfonlineb.fqnovel.com` | POST JSON + query 设备参数 + Gorgon 签名（`provider_hongguo_app.go:hongguoAppRequest:137`） | 目录（分类 feed）、推荐、详情、分集、App 取流、弹幕 |

另外还有一条**第三方兜底取流**：`https://djapi.999888456.xyz/api/hongguo/play`（`provider_hongguo_playback.go:26`），仅在 Web 与 App 取流都失败时使用。

端点速查（均为 guoapp 实际调用的路径）：

| 端点 | 方法 | 所在 |
|---|---|---|
| `/reading/distribution/category/landpage/v/` | POST | App 目录/推荐 feed（`provider_hongguo_catalog.go:127`、`provider_hongguo_recommendations.go:75`） |
| `/novel/player/video_detail/v1/` | POST | App 详情+分集（`provider_hongguo_detail.go:50`）；封面元数据复用（`app_cover_metadata.go:24`） |
| `/novel/player/video_model/v1/` | POST | App 取流（`provider_hongguo_native_media.go:23`） |
| `/novel/commentapi/comment/list/<videoID>/v1/` | POST | 弹幕（`ui_playback_danmaku.go:96`） |
| `hongguoduanju.com/incent_resource/suggestion` | GET | 搜索联想/名称索引（`provider_hongguo_suggestions.go:143`） |
| `hongguoduanju.com/category/<route>?page=N` | GET | Web 分类（`provider_hongguo.go:110`） |
| `hongguoduanju.com/detail?series_id=<id>` | GET | Web 详情（`provider_hongguo.go:150`） |
| `hongguoduanju.com/player/<seriesID>/<videoID>` | GET | Web 播放器页取流（`provider_hongguo.go:229`） |
| `hongguoduanju.com/search/<keyword>` | GET | Web 搜索（`provider_hongguo_search.go:294`） |
| `hongguoduanju.com/rank/<board>?page=N` | GET | Web 排行榜（`provider_rankings_hongguo.go:26`） |
| `djapi.999888456.xyz/api/hongguo/play?id=<base64>` | GET | 兜底取流（`provider_hongguo_playback.go:44`） |

ID 规则（全局贯穿）：剧 ID = `hongguo:<seriesID>`，章节 ID = `hongguo:<seriesID>:<videoID>`（`provider_huangguo.go:providerDramaID:75/providerChapterID:79`）；纯数字 ID 合法性由 `^[0-9]{1,32}$` 校验（`provider_hongguo_playback.go:hongguoNumericID:31`）。章节的 `VideoURL` 字段实际存的是伪协议 `hongguo-cenc://<videoID>`（`provider_hongguo.go:170`、`provider_hongguo_detail.go:95`），取流时再解析。

---

## 1. 分类 / 筛选 / 排序的请求参数面

### 1.1 App 目录 feed（主通道）

**端点**：`POST /reading/distribution/category/landpage/v/`，JSON body（`provider_hongguo_catalog.go:108-119`）：

```json
{
  "req_scene": "default|comic_series|ai_series",
  "offset": 0,
  "limit": 18,
  "req_type": "only_content",
  "need_selector_panel": false,
  "client_req_type": 3,          // offset>0 时改传 2
  "session_id": "<服务端会话>",
  "filter_ids": "",              // 推荐接口用来传已看 ID 逗号串（provider_hongguo_recommendations.go:59,66）
  "select_items": {
    "genre": ["short_play"],     // 或 comic_series / ai_series
    "sort": ["online_time"],
    "gender": [],
    "category_dim_theme": [],
    "category_dim_role": [],
    "category_dim_epoch": [],
    "online_time": [],
    "creation_status": []
  }
}
```

**可筛/可排序维度**：`select_items` 就是官方的筛选面板 schema（`need_selector_panel=false` 表示不要返回面板定义）。guoapp 实际只用了两个维度：`genre`（3 个值，即内容大类）与 `sort=["online_time"]`（上线时间序）。schema 里暴露但未使用的维度：`gender`（性别向）、`category_dim_theme/role/epoch`（题材/角色/年代三维标签）、`online_time`（上线时间段）、`creation_status`（连载状态）——这些数组留空，可筛选值需抓包或设 `need_selector_panel=true` 观察响应确认（**推测可从面板响应拿到全部枚举**）。

genre 与 scene 的固定映射（`provider_hongguo_catalog.go:15-22`）：`short_play→default（真人剧）`、`comic_series→comic_series（漫剧）`、`ai_series→ai_series（AI剧）`。UI 侧分类列表就是这 3 项 + 全部（`app_categories.go:nativeCategories:88`）。

**分页方式**：`offset + session_id` 游标，不是页码。响应 `data.next_offset / has_more / session_id`（`provider_hongguo_catalog.go:parseHongguoCatalogPage:196`）。每页 `limit=18`。客户端把游标持久化为 `hongguoCatalogCursor{Offset, SessionID, LastID, PageSignature, Initialized, Exhausted, UpdatedAt}`（`provider_hongguo_app.go:33`），并做防御：`next_offset` 必须**严格前进**、页签名（本页排序后 ID 的 sha256）不得与上页相同、`session_id` ≤4096 且 30 分钟未用即作废重开会话（`provider_hongguo_catalog.go:106-108、218-240`）。游标持久化到 `catalogs.json`（`app_catalog_cache.go:nativeCatalogDisk`，`HongguoApp` 字段），重启后按 scene 恢复（`app_catalog_persistence_test.go:TestNativeHongguoCursorRestartsPerCatalog`）。

**翻页上限**：`MaxPagesPerSort` 默认 50、封顶 200（`app_runtime.go:defaultConfig:76`、`provider_hongguo_catalog.go:61-66`）；「已初始化后」只刷头 3 页做增量，除非用户显式加载更多（`headLimit:68`、`libraryMoreKey`，`provider_hongguo_catalog.go:35,68-71,90-110`）。

**推荐 feed**：同端点、不同调用面（`provider_hongguo_recommendations.go:fetchHongguoRecommendations:42`）：`sort` 留空、`filter_ids` 传已见剧集 ID（≤540 个）去重，`query.Seen` 逐个校验数字 ID。这是 UI「推荐」页的数据源，也证明 `filter_ids` 是官方支持的服务端去重参数。

### 1.2 Web 分类（兜底通道）

`GET /category/<route>?page=N`，route ∈ {`real-drama`,`comic-drama`,`ai-drama`,`comic`}（`provider_hongguo.go:38-43`）。响应在 `_ROUTER_DATA.loaderData["category_page…"].recommendList`，分页 `pagination.totalPages`（`provider_hongguo.go:fetchHongguoCategoryPage:110`）。**页码分页**，每页约 24 条（`len(items) < 24` 即停，`provider_hongguo.go:104`），页数封顶 500。此通道无任何筛选/排序参数——只有大类路由。

### 1.3 客户端本地排序/过滤（不发出请求）

目录拉回后，guoapp 在 Flutter 侧做二次排序与过滤（`lib/catalog_sort.dart:sortCatalog`）：排序维度 = 源顺序（默认）/ 名称 / 自然季号 / 上线日期 / 热度 / 播放量（`CatalogSort` 枚举，`catalog_sort.dart:3-11`）；过滤维度 = 完结状态（finished/ongoing/unknown，`CatalogView.release`）。热度/播放量文本解析支持 `亿/万/千/w/k/m/b` 单位（`catalogMetrics`，`catalog_sort.dart:110`）。重写版可以直接复刻这套语义（纯前端），但要注意它是排序本地已缓存目录，不是服务端能力。

---

## 2. 搜索与联想

搜索 = **官网 SSR 搜索页 + 名称索引（联想接口）双通道合并**，再加季数补齐（`provider_hongguo_search.go:fetchHongguoSearch:149`）。

### 2.1 官网搜索页

`GET https://hongguoduanju.com/search/<url-escaped-keyword>`，Referer `hongguoduanju.com/`，UA 为 iPhone Safari（`config_defaults.go:userAgent:6`）。响应解析 `_ROUTER_DATA.loaderData["search_(keyword)/page"]`（前缀匹配兜底 `search_`）：`isSuccess` 必须 true、`query` 必须等于请求词，结果在 `searchList[]`，每行含 `video_data` 卡片（§3.1），总数 `totalCount`（`provider_hongguo_search.go:fetchHongguoSearchPage:294`）。

**没有分页**：官网搜索页一次 SSR 返回全部结果；`entry.Limited = totalCount > len(Dramas)` 只用于提示截断（`provider_hongguo_search.go:322`）。Flutter 侧 `SourceSite.pagedSearch` 也确认 hongguo 不分页（`lib/models.dart:11-17`）。

### 2.2 名称索引（联想端点当搜索用）

`GET hongguoduanju.com/incent_resource/suggestion?app_id=8662&query=<kw>&count=<N>`（`provider_hongguo_suggestions.go:fetchHongguoSuggestionRecords:139`）。响应 `{"suggest_list":[…]|"data":{"suggest_list":[…]}}`（根/嵌套两种形态都兼容，`app_suggestions_test.go:TestNativeSuggestionsAcceptRootAndEmptyResults`）。每条记录字段（`provider_hongguo_suggestions.go:28-33`）：

```json
{ "name": "剧名", "word_type": "short_play_name", "keyword": …, "video_data": {…完整卡片…} }
```

- **联想场景**：`count=10`，白名单 `word_type ∈ {short_play_name, short_play_category, common_query, actor_name, short_play_actor}`（`provider_hongguo_suggestions.go:111-133`）。
- **名称索引场景**：`count=50`，只取 `word_type=="short_play_name"` 且卡片里有数字 series_id 的记录转成剧卡（`provider_hongguo_search.go:fetchHongguoSearchNames:229`）。这条通道返回的 `video_data` 是完整卡片（标题/封面/集数/热度），是 guoapp 搜索结果的主力来源之一。

### 2.3 防抖与并发控制

- **客户端防抖**：Flutter `SearchInput` 300ms 定时器 + 本地 LRU 缓存 32 条 + 生成号丢弃过期响应（`lib/search_input.dart:69-84`）。联想只对 hongguo 启用（`lib/models.dart:SourceSite.searchSuggestions:18`）。
- **Go 侧合并**：联想与搜索都有 per-key singleflight（同词并发只发一次请求，其余等待共享结果，`provider_hongguo_suggestions.go:62-108`、`provider_hongguo_search.go:searchHongguoDramasProgress:62`）；联想缓存 TTL 2 分钟、上限 128 词；搜索结果缓存 TTL 5 分钟、上限 64 词；pending 上限各 32（`provider_hongguo_suggestions.go:11-19`、`provider_hongguo_search.go:25-29`）。
- **搜索预算**：总超时 40s，搜索页单独 12s，联想请求 5s（`provider_hongguo_search.go:25-28`、`provider_hongguo_suggestions.go:19`）。

### 2.4 结果加工与季数补齐

- 双通道结果按剧 ID 去重合并（`mergeHongguoSearchDramas`，`provider_hongguo_search.go:210`）；本地相关性排序：精确=0 / 前缀=1 / 包含=2 / 全词命中=3 / 其他=4（`hongguoTitleSearchRank:269`），归一化 NFKC + 去空白标点 + 小写（`hongguoSearchText:256`）。
- **季数补齐**：识别标题尾部 `第N季/部`（中文数字支持，`hongguoSearchSeasonSuffix`，`provider_hongguo_search_seasons.go:19`），发现同系列有缺口时自动追加查询 `《剧名》+第X季`（中文/阿拉伯数字两种写法），预算 32 次查询、25 秒（`provider_hongguo_search_seasons.go:22-25、completeHongguoSearchSeasons:196`）。这是纯客户端行为，不依赖特殊端点。

---

## 3. 详情与分集列表的数据结构

### 3.1 剧卡片（目录/搜索/详情共用）

统一解析函数 `hongguoDramaFromAny`（`provider_hongguo.go:255`）。输入兼容两种形态：外层直接是卡片（`m`），或 `{"video_data": {…}}` 包一层（App feed 风格）。关键字段（按 guoapp 消费的顺序，多写一个候选名表示都试）：

| 业务字段 | 上游 JSON 键 | 说明 |
|---|---|---|
| 剧 ID | `video_data.series_id_str` / `series_id`（兜底 `keyword`） | 必须是纯数字，否则整卡丢弃 |
| 标题 | `series_title` / `series_name` / `title` | |
| 简介 | `series_intro` / `video_desc` | |
| 封面 | `series_cover` / `cover` | 协议补全 `//` → `https:`（`app_cover_metadata.go:hongguoCoverAddress:136`） |
| 集数 | `episode_cnt` | 文本型数字 |
| 角标/备注 | `episode_right_text` | 兜底生成「共N集」 |
| 完结状态 | `series_status`：`"1"`=完结、`"0"`=连载 | 优先于 `episode_right_text` 推断（`releaseStatusFromRemark`） |
| 标签 | `tags`（字符串数组）+ `category_list[].name` + `category_schema`（JSON 字符串 `[{name}]`） | 三处合并去重 |
| 一级分类 | `category_name` / `categoryName` / `category` | 缺省取第一个 tag |
| 评分 | `score` | 文本 |
| 播放量 | `series_play_cnt` / `play_cnt` | 文本 |
| 热度 | `hot_score_data.score`（数值文本）优先，`hot_score_data.text` 兜底 | `provider_sort_metadata.go:hongguoHeat:12` |
| 上线日期 | `first_visible_time`（时间戳）优先；否则 `sub_title_list[].content` 的「今日/昨日/X月X日上新」文本换算（东八区） | `provider_sort_metadata.go:hongguoOnlineDate:24` |

### 3.2 App 详情 + 分集列表

`POST /novel/player/video_detail/v1/`，body `{"series_id": "<seriesID>"}`（`provider_hongguo_detail.go:50`）。响应 `data.video_data` 是完整卡片（§3.1），另有分集数组 `video_list[]`，每项：

```json
{ "vid": "800001", "vid_index": 1, "series_id": "700001" }
```

解析与防御（`provider_hongguo_detail.go:parseHongguoAppDetail:70`）：
- `vid`、`vid_index` 必须合法（数字、index ≥1），`series_id` 必须与请求一致（防串剧）；重复 vid/index 直接报错；
- `episode_cnt` 必须等于实际返回数，且分集序号必须连续 1..N，否则视为不完整走 Web 兜底；
- 产出 Chapter：`Title=第N集`、`VideoURL="hongguo-cenc://<vid>"`、`CurrentEpisode=N`，按序号排序。
- 结果缓存 5 分钟、上限 128 条（`provider_hongguo_detail.go:25-61`）。

### 3.3 Web 详情（兜底）

`GET /detail?series_id=<id>`，loader `detail_page`/`detail_`，取 `seriesDetail`：标题 `series_name`/`series_title`/`name`，分集 `vid_list[]`（纯 vid 数组，按序号即数组下标+1），同样产出 `hongguo-cenc://` 章节（`provider_hongguo.go:fetchHongguoWebChapters:150`）。整体链路：先 App 详情、失败回落 Web（`provider_hongguo.go:fetchHongguoChapters:131`）。

---

## 4. 取流：播放地址、画质档位、线路选择

### 4.1 三级回退链路

`resolveHongguoMedia`（`provider_hongguo.go:192`）顺序：**Web 播放器页 → App API → 第三方兜底**。选 Web 优先是刻意的（v10 fix 注释，`provider_hongguo.go:198-201`）：App API 的 variant 多为字节私有编码 `bytevc1/bytevc2`，Android MediaCodec 解不了；Web 播放器返回标准 h264 MP4。所有线路失败时合并三条错误信息返回。

### 4.2 Web 播放器页取流

`GET hongguoduanju.com/player/<seriesID>/<videoID>`。loader `player_page`/`player_`，校验返回的 `vid`/`series_id` 与请求一致（不一致视为「仅允许网页试看」，`provider_hongguo.go:236`）。媒体信息在 `video_player_info`：`duration`（秒，字符串）+ 地址族。地址提取键序：`main_url`、`backup_url`、`backup_url_1`、`backup_url_2`、`backup_urls`、`url_list`（`provider_hongguo_native_media.go:hongguoMediaAddresses:126`）；值可能是明文 http(s) URL 也可能 base64（自动探测解码）；每个地址生成一个 variant。**Web 地址不加密（无 CENC 密钥）**，Referer 固定 `hongguoduanju.com/`（`provider_hongguo.go:247`）。

### 4.3 App API 取流

`POST /novel/player/video_model/v1/`，body（`provider_hongguo_native_media.go:17-20`）：

```json
{ "video_id": "<vid>", "content_type": 1, "biz_param": { "need_all_video_definition": true, "video_platform": 3 } }
```

响应 `data.video_model`（map 或 JSON 字符串，两种都兼容）内 `video_list[]`，每档 variant：

- 地址：同 §4.2 地址族键；
- `video_meta`：`codec_type`（`h264`/`hevc`/`h265_hvc1`/`bytevc1`/`bytevc2`…）、`vheight`/`vwidth`、`definition`（如 `"720p"`/`"1080p"`/`"2160p"`）；
- `video_duration`/`duration`：整集时长秒；
- `encrypt_info`：`spade_a`（混淆后的密钥材料）、`encrypt`（bool）、`encryption_method`（`"cenc-aes-ctr"`）→ 触发 CENC 密钥提取（§6.3）；
- `gear_des_key`：码率档描述链（如 `0:MP4|1:encrypt|2:bytevc2|4:1080p|…`），**注意其中含 `bytevc2` 字面量不代表该档是 bytevc2**（v8 fix，判定只看 `video_meta.codec_type`；回归测试 `app_test.go:TestHongguoCodecFilterPreservesHevenWhenGearKeyMentionsBytevc2:176`）。

**画质判定**：`definition` 数字 > `vheight` > 短边 `vwidth`（`provider_hongguo_native_media.go:89-93`）。**档位打分**：`height*10`，`h264/avc1` 再 +1；`bytevc1/bytevc2` 档降权 -100000 且 `Quality=0`（v11 策略：不硬 ban，留作最后尝试，`provider_hongguo_native_media.go:60-106`）。测试断言 h264-720p < hevc-1080p（`app_test.go:TestHongguoDetailAndPlayableQuality:146`）。

### 4.4 第三方兜底 API

`GET https://djapi.999888456.xyz/api/hongguo/play?id=<base64(JSON)>`，JSON 为 `{"content_type":1004,"from_video_id":"","series_id":…,"vid":…,"video_platform":3}`（`provider_hongguo_playback.go:32-47`）。响应两种形态：
- 明文 JSON；
- `v2.<hex>.<base64>` 加密信封：自定义流混淆派生 AES 密钥材料 → AES-128-CBC 解密 + PKCS7 去填充（`decodeHongguoPlaybackResponse:112`）。

解出的 JSON：`parse`/`jx` 必须为空值（表示「直接媒体地址」而非解析型），`key_urls[] = {name, src(URL), kid(16 字节 hex), spade_a}`；`name` 里的数字当画质（如 `1080p`），每项一个 variant，选最高画质（`provider_hongguo_playback.go:60-106`）。`kid` 必须 hex 且 16 字节，`spade_a` 走 §6.3 提取 AES-128 密钥。

### 4.5 画质切换与线路（Route）选择

- 所有 variant 汇总进 `providerMedia.Variants`；`nativePlaybackChoices`（`app_playback_routes.go:24`）按 `URL+Referer+CENCKey+HLSKey` 指纹去重、按 `Quality` 降序排列，形成「线路列表」；若用户指定画质且存在该档，则过滤只留该档。
- 线路切换 = 列表 index + 1（`nativeNextPlayback`，`app_playback_routes.go:158`）；`plan.RouteIndex/RouteCount` 告知 UI 当前第几条线路；同时会话上限 8 个（`nativePlaybackLimit`，`app_playback_routes.go:12`），过期 12 小时。
- 画质候选列表 `plan.Qualities`（去重降序），Flutter 端 `_requestedQuality` 用它做清晰度菜单。

### 4.6 媒体 URL 时效与刷新

红果 CDN 直链带签名，约 30 分钟过期（v3 fix 注释，`provider_hongguo.go:351`）。本地流代理 `app_stream.go:439-465`：上游回 403/410 且会话留有 `seriesID/videoID` 时，5 秒超时调 `refreshHongguoMediaURL`（`provider_hongguo.go:353`，内部走 §4.4 兜底 API 重取）换新 URL 继续供流。注意：刷新走的是兜底 API 而非原线路。

### 4.7 媒体请求头

- App/Web API 请求头见 §5；媒体字节流请求（CDN）用 `mediaRequestHeaders`（`network_media.go:25-40`）：iPhone UA、`Accept: */*`、`Accept-Language: zh-CN,zh;q=0.9`、`Sec-Fetch-Mode: cors`、`Sec-Fetch-Dest: empty`；带 Range 时 `Accept-Encoding: identity`。
- **Referer 策略（v9）**：App/兜底线路的 CDN URL **不带 Referer**（curl 实测：无 Referer = 206，写 `hongguoduanju.com` = 403 denied by Referer ACL，`provider_hongguo_native_media.go:77-78`、`provider_hongguo_playback.go:93-94` 注释）；Web 线路带 `hongguoduanju.com/`。V4A 注释明确不再注入 Origin/自造跨域头（`network_media.go:24-27`）。

---

## 5. 请求签名 / 设备参数 / headers

### 5.1 App API 公共 query（设备身份）

`hongguoAppRequest`（`provider_hongguo_app.go:137-148`）每个请求都带：

```
aid=8662  app_name=novelread  version_code=73532  version_name=7.3.5.32
manifest_version_code=73532  update_version_code=73532  channel=update_64
device_platform=android  os=android  ssmix=a  device_type=25053RT47C  device_brand=Redmi
language=zh  os_api=36  os_version=16  resolution=1280*2772  dpi=520  ac=wifi
device_id=<随机19位数字>  iid=<随机19位数字>  _rticket=<请求时刻毫秒>
```

- `device_id`/`iid`：`newHongguoDeviceID`（`provider_hongguo_sign.go:15`）= `1e18 + rand64 % 8e18`，即 19 位十进制；进程内固定一对、随目录游标持久化（`provider_hongguo_app.go:61-68`、`restoreHongguoCatalog:86` 校验后才恢复）。
- `_rticket` 在每次（含重试）发送前刷新（`provider_hongguo_app.go:197-200`）。

### 5.2 App API 公共 headers

```
User-Agent: com.phoenix.read/70000 (Linux; U; Android 12; zh_CN; PFZM10; Build/SP1A.210812.016; Cronet/TTNetVersion:04270129 2024-01-15 QuicVersion:5e92d3a0 2023-08-23)
Accept: application/json
X-XS-From-Web: 0
Sdk-Version: 2
Content-Type: application/json; charset=utf-8     (仅 POST body)
```

UA 常量 `hongguoAppUserAgent`（`provider_hongguo_app.go:29`）。v11 注释（`:23-28`）：故意降到 `com.phoenix.read/70000 + Android 12` 老版本——疑似服务端对最新 UA 全吐 bytevc（付费墙策略），老 UA 可能拿到 h264；有被 403 的风险。重写版建议把 UA 做成可配置项。

### 5.3 普通请求签名（X-Gorgon 族）

`signHongguoRequest`（`provider_hongguo_sign.go:23`）产出 4 个头：

- `X-Khronos`：unix 秒。
- `X-SS-Req-Ticket`：unix 毫秒。
- `X-SS-STUB`：POST body 的 MD5 大写 hex（无 body 不发）。
- `X-Gorgon`：`hex(0x84 0x04 0x40 0x1c 00 00 + payload20)`。payload20 构造：`md5(RawQuery)[0:4]` + `md5(body)[4:8]`（无 body 留空）+ 常量 `{0,6,11,28}`@`[12:16]` + 时间戳 BE32@`[16:20]`；先与 20 字节固定 key 逐字节 XOR，再做两轮 `rotl4(x)^next` / `reverse8(x)^0xff^20` 混淆。这是字节系 Gorgon 0x8404 版本的简化复刻，**只对 query+body+时间戳敏感**，不含路径。

普通请求**不带** `X-Argus`/`X-Ladon`/`Comment-*`（测试断言 `app_danmaku_test.go:88-90`）。重试策略：1..3 次、线性退避、4xx 直接放弃（`provider_hongguo_app.go:160-228`）；响应体上限 20MB（`providerMaxBodyBytes`，`provider_huangguo.go:35`），业务码 `code/status_code/BaseResp.StatusCode` 非 0 视为失败。

### 5.4 弹幕（评论）请求签名

弹幕请求在 ctx 打 `hongguoCommentKey{}` 标记（`provider_hongguo_app.go:171-192`、`ui_playback_danmaku.go:90`），改走 `signHongguoCommentRequest`（`provider_hongguo_comment_sign.go:27`）：

- 额外 headers：`Comment-Source: 601`、`Server-Channel: 1000`，且**匿名（无 Cookie）**（测试断言 `app_danmaku_test.go:99-101`）。
- `X-Gorgon`：同一 Gorgon 骨架但 key 含 11 字节随机 nonce 派生的 `head2/head3`，前缀 `0x84 4 head2 head3 0 0`；payload 混入 `md5(query)[0:4]` + 常量 `{0,1,7,4}` + 时间戳，经 RC4 风格 S 盒（KSA 用 8 字节 key）再混淆。
- `X-Ladon`：`hongguoCommentLadon`（`:73`）——`md5(nonce[2:6] + aid)` 的 hex 摘要做 34 轮 64 位密钥流，对固定串 `"<ts>-1611921764-3019"` 做类 TEA 轮函数（rotl/加法混合）+ PKCS7，输出 `base64(nonce[2:6] + data)`。
- `X-Argus`：`hongguoCommentArgus`（`:99`）——构造 protobuf 风格消息（字段 1-21：版本 `0x20200929`、随机数、`"3019"`、`"1611921764"`、`"6.8.1.32"`、`"v04.07.01-ml-android"`、时间戳、body/query 的 SM3 摘要前 6 字节等），用固定 key `ac1adaae…c28c` 派生 72 轮 Feistel 轮函数加密，再 AES-128-CBC（key/iv = md5(key 前半/后半)）封装，base64 输出。SM3 为本地实现（`hongguoSM3:168`）。

> 重写版注意：`X-Argus/X-Ladon` 是完整复刻字节风控的一部分实现成本很高；guoapp 的经验是**弹幕接口只需要这一套**（Comment-Source/Server-Channel + 三签名头），目录/详情/取流只需 §5.3 的轻量 Gorgon。可以先按 guoapp 的实现逐字节照抄。

### 5.5 Web 侧 headers

SSR 页面请求（`fetchProviderText`，`provider_huangguo.go:353`）：iPhone Safari UA（`config_defaults.go:6`）、`Referer: hongguoduanju.com/`（provider 域名时自动带上）、`Accept-Language: zh-CN,zh;q=0.9`。联想端点额外 `Accept: application/json`（`provider_hongguo_suggestions.go:148-152`）。

---

## 6. 媒体封装、加密与下载解密链路

### 6.1 红果媒体的形态

- **Web 线路**：标准 MP4 直链（ffprobe 验证为 h264 High profile，v10 注释 `provider_hongguo.go:198-201`），**无加密**。
- **App 线路**：MP4/fMP4 直链 + `encrypt_info.encryption_method="cenc-aes-ctr"`（CENC，MPEG-DASH 通用加密 AES-CTR 模式），密钥藏在 `spade_a`（`provider_hongguo_native_media.go:79-84`）。guoapp 没有给红果用 HLS master playlist——`hongguo-cenc://` 章节解析出的是单文件直链；`.m3u8` 处理逻辑（§6.4）服务于其他站源。
- CDN URL 30 分钟签名过期（§4.6）。

### 6.2 CENC 内容密钥提取（`hongguoContentKey`）

`provider_hongguo_playback.go:163`：`spade_a` 是 base64 的自定义混淆串：
1. base64 解码（兼容 Std/Raw padding，`decodeHongguoBase64:154`）；
2. 末尾 tag 长度藏在 `raw[0]^raw[1]^raw[2] - 48`，tag 用前一字节 XOR 种子还原；tag 为 `app_v2`/`web_v2` 的**新版本格式不支持**（报错）；
3. 内容区做「奇偶链式 XOR - 21 - popcount(index)」反混淆；
4. 首字符是 base36 padding 长度，去掉后必须恰好剩 32 个 hex 字符；
5. hex 解码成 16 字节 = AES-128 内容密钥。

兜底 API 的 `key_urls[].kid`（16 字节 hex）与 `spade_a` 同源校验（`provider_hongguo_playback.go:78-86`）。

### 6.3 在线播放的解密层

Go 层**不做** CTR 解密，只把密钥以 hex 传给 Flutter（`app_playback_routes.go:79` `plan.Key`）。真正解密在播放器：
- Flutter 端用 media_kit（libmpv）播放，`demuxer-lavf-o=…,decryption_key=<hex>`（`lib/player_screen.dart:1034-1044`）——即 **ffmpeg 的 mov/mp4 解封装器在 demux 层用 ClearKey 解 CENC 样本**。
- 注释明确：media_kit 是唯一能解红果 CENC 的路径，video_player/ExoPlayer 拿到加密数据只会卡住（`lib/player_screen.dart:190-192`）。
- 本地流代理（`app_stream.go`）只做 Range 转发、URL 改写与 403/410 刷新，不碰密文。

### 6.4 下载：HLS 分段处理（其他源通用，红果 MP4 亦经此管线）

`downloadBundle`（`app_download_hls.go:102`）：
- master playlist 按目标画质选档：优先 `RESOLUTION` 高度精确匹配，其次高度大者、再次 `BANDWIDTH` 大者（`nativeHLSSelect:26`）；保留所选档的 `#EXT-X-MEDIA` 组（音频/字幕），剔除其余 variant、I-FRAME、SESSION-DATA；
- 深度 ≤6、资产 ≤20000、必须是 VOD（无 `#EXT-X-ENDLIST` 即拒收，`:140`）；
- `#EXT-X-KEY` 只支持 `METHOD=AES-128|NONE` 且 `KEYFORMAT` 必须为空或 `identity`（SAMPLE-AES 等拒收，`:204-212`）；密钥 URI 改写为本地 `.key` 文件——若上层已知密钥（`providerMedia.HLSKey`）直接落盘，`data:` 内嵌 key 也可解析（`:156-176`）；
- 所有 URI 改写为 `sha256(url)[:16]` 本地文件名，playlist 重写后与分片一起存（`nativeDownloadAssetName:97`）。

红果 MP4（非 HLS）下载走 `transferMedia` 的单文件分支：原样存 `media.mp4`（**密文**），CENC 密钥 hex 记录在下载索引（`nativeDownloadResult.key`，`app_download_transfer.go:121-122`；`.bundle` 指纹里也含密钥，`:59`）。

### 6.5 「下载合并后无加密」的答案

合并/导出/本地播放统一走 ffmpeg，**解密发生在 ffmpeg 解封装层，不在 Go 也不在 Dart**：
- 本地播放：同一个 mpv `decryption_key` 路径（`media_library.dart:421-423` 同样把 key 传给 ffmpeg probe）。
- 合并/导出：`ffmpeg -decryption_key <hex> -i … -map 0:v:0 -map 0:a:0? -c copy …`（`lib/media_library.dart:421-449`）。`-c copy` 看似不重编码，但 CENC 加密在样本（sample）层——mov demuxer 解出样本时已用 key 解密，remux 写出的就是**明文** mp4/mkv。因此「下载的是密文、合并产物无加密」，链路 = Go 存密文+key → Dart 调 ffmpeg 带 key 解封 → 明文落地。
- 离线回归测试：`app_download_decode_test.go:TestNativeDownloadedEncryptedHLSDecodesWithSourceOffline`（合成 AES-128 HLS，断言离线可解、且不会再回源取 key）。
- 合并指纹把密钥纳入 checkpoint（`media_merge.dart:140`），key 变了会拒绝续传旧任务。

---

## 7. 弹幕 API

### 7.1 端点与请求

`POST https://api5-normal-sinfonlineb.fqnovel.com/novel/commentapi/comment/list/<videoID>/v1/`（`ui_playback_danmaku.go:96`），body：

```json
{
  "comment_source": 601, "server_channel": 1000,
  "group_id": "<videoID>", "group_type": 30,
  "comment_type": 20, "sort": 1, "count": 90, "cursor": "",
  "aid": 8662, "compliance_status": 0,
  "business_param": {
    "book_id": "<seriesID>",
    "start_offset_time": <窗口起点 ms>,
    "playlet_item_duration": <窗口终点 ms（本集时长）>,
    "need_danmaku_guide_type": [1, 3, 4, 2]
  }
}
```

（请求体 `ui_playback_danmaku.go:92-95`；头注入 `provider_hongguo_app.go:184-192`；测试佐证 `app_danmaku_test.go:104-121`。）headers：§5.4 评论签名全套（`Comment-Source: 601`、`Server-Channel: 1000`、X-Gorgon/X-Khronos/X-SS-Req-Ticket/X-Argus/X-Ladon，`X-SS-STUB` 置空），无 Cookie。

### 7.2 响应结构与解析

```json
{ "code": 0, "data": {
  "data_list": [ { "comment": {
      "comment_id": "…",
      "common":  { "group_id": "<videoID>", "status": "1", "content": {"text": "…"} },
      "expand":  { "offset_time": 15000 }
  } } ],
  "extra":           { "next_query_danmaku_list_time": 30000 },
  "common_list_info":{ "cursor": "{\"danmaku_count\":250}", "has_more": false, "total": 5 }
} }
```

解析规则（`parseHongguoDanmaku`，`ui_playback_danmaku.go:124`）：
- `next_query_danmaku_list_time` 必须存在且 > 当前窗口起点，否则整页无效（时间分页游标）；
- 只收 `group_id` 与请求 videoID 相同、`status=="1"`、`offset_time` 落在 `[start, next)` 的条目；
- 文本清洗：控制字符/行分隔符替换为空格；超 180 rune 截断加省略号；每页最多 90 条（与请求 `count` 对齐）；按 `offset_time` 排序；
- `common_list_info.cursor` 是**内嵌 JSON 字符串**，`danmaku_count` 即本集总弹幕数（`page.Total`）；
- 页结构（对前端）：`{episodeId, items[{id,text,timeMs}], startMs, nextMs, total}`（`ui_playback_danmaku.go:19-31`）。

### 7.3 窗口、缓存与并发

- 窗口固定 30 秒（`danmakuWindowMS`，`ui_playback_danmaku.go:16`）；`duration` 上限 24h（`:17`）。
- 会话绑定：弹幕身份（seriesID/videoID）来自播放计划而非 URL——`hongguoPlaybackIDs`（`:44`）从 `hongguo-cenc://` 章节提取，跨线路切换（`nativeNextPlayback`）保持同一集（测试 `app_danmaku_test.go:TestNativeDanmakuUsesPlaybackIdentityAcrossRoutes:185`）；播放会话 12h 过期。
- 缓存：per `(series,video,start,duration)` 5 分钟成功缓存 / 15 秒失败缓存，上限 128；singleflight 合并并发，pending 上限 16（`ui_playback_danmaku.go:53-118`）。弹幕请求标记为后台低优先级（`backgroundCatalogKey`）。

---

## 8. guoapp 排行榜：能力边界与官方 App 差异面

### 8.1 guoapp 实现的榜单

数据源是**官网 SSR 页**，不是 App API：`GET hongguoduanju.com/rank/<path>?page=N`（`provider_rankings_hongguo.go:fetchHongguoRankingPage:18`）。四个固定榜单（`provider_rankings.go:rankingBoards:24-27`）：

| ID | 名称 | path | upstreamKey |
|---|---|---|---|
| hongguo-hot | 总热播榜 | hot-drama | hongguo |
| hongguo-real | 真人剧榜 | hot-real-drama | real |
| hongguo-comic | 漫剧榜 | hot-comic-drama | comic |
| hongguo-ai | AI剧榜 | hot-ai-drama | ai |

描述文案标注「每日更新」（`provider_rankings.go:25-27`），页面另有 `updatedText`（如「9月13日已更新」）。

**数据提取三级兜底**（`parseHongguoRanking`，`provider_rankings_parse.go:39`）：
1. `_ROUTER_DATA.loaderData["rank_<path>/page"].content` 内联 JSON（校验 `rankKey` 匹配 upstreamKey、`pageNum` 匹配页码）；
2. `mergeLoaderData` script 标签（`data-fn-name="mergeLoaderData"` + `data-script-src="modern-run-window-fn"`，`provider_rankings_loader.go:8`）；
3. `modern-run-router-data-fn` script 标签（`data-fn-args` 三元组）；
4. 全失败则回退解析 HTML 文档（canonical link、`nav[aria-label=榜单分页]`、`article[aria-labelledby^=rank-title-]`）或 JSON-LD `ItemList`（`provider_rankings_hongguo.go:88-279`）。

行结构（loader JSON，`provider_rankings_parse.go:15-33`）：`rankList[] = {id, seriesId, rank, title, heatText("1.2亿热度"), scoreText("评分9.2"), tags[], description, episodeVids[], cover}` + `pagination{pageNum, totalPages}`。

**严格校验**（防 SSR 不完整/结构漂移）：rank 必须 `(page-1)*20 < rank ≤ page*20` 且严格递增、ID 数字且不重复、id/seriesId 一致、页 ≤500、每页 20 条（`provider_rankings_parse.go:76-90`）；HTML 路径还校验 canonical 与 aria-current 页码。解析失败区分「未完整」（errHongguoRankingIncomplete，重试 3 次带平方退避）与「格式变化」（errHongguoRankingFormat，直接报错）。
缓存：内存 5 分钟 TTL + 磁盘 `rankings.json`（≤8MB、24h 内可当 stale 兜底，`provider_rankings.go:99-160`、`app_rankings.go:loadRankingCache:20`）。榜单条目刻意**剥离封面图片**（`rankingDramaWithoutImages`，`provider_rankings.go:91`；测试断言 `provider_rankings_test.go:42`），封面走懒加载详情链路。

### 8.2 能力边界（相对官方 App 的已知缺口）

guoapp 榜单**只有**上述 4 个官网公开榜，且：
- 无用户/个性化维度（性别向、地区、推荐流混排）；
- 榜单周期只有页面给的粒度（文案「每日更新」），没有日/周/月切换参数；
- 热度只有文本 `heatText`（如「1.2亿热度」），拿不到数值热度分；
- 无短视频/电影/有声/书籍等其他内容形态的榜（官网 rank 路由或许有，guoapp 未接）;
- 无「飙升/黑马/新书/口碑」等运营位榜单；
- 每页固定 20 条、页码上限 500，无时间范围/分类交叉筛选。

### 8.3 官方最新 App 排行榜的差异面（抓包对照清单，均为**推测**）

官方 App 的榜单大概率不走 `hongguoduanju.com/rank/*`，而是走 `api5-normal-sinfonlineb.fqnovel.com` 的字节系 feed（与 §1.1 目录同族的 `/reading/...` landpage/lp 接口）。抓包对照时建议关注：

1. 榜单请求路径与参数：是否复用 `/reading/distribution/category/landpage/v/`（`sort` 换成榜单 key）或有独立 `/reading/book/rank/...`、`lp_source` 类端点；
2. `select_items.sort` 的合法枚举：guoapp 只用过 `online_time`，榜单大概率有 `hot_score`/`rise`/`new` 等值——这是补齐 §1.1 排序维度的最直接证据；
3. 榜单条目卡片是否复用 `video_data` 结构（`series_id_str`/`hot_score_data` 等，§3.1），还是榜单专属 schema（含数值热度、上升趋势箭头）；
4. 数值热度字段：`hot_score_data.score` 在目录卡片已出现（`provider_sort_metadata.go:12`），榜单响应里可能有同名或 `heat_value` 类字段；
5. 周期参数：`rank_period`/`time_range` 类 query 或 body 字段；
6. 分页方式：offset+session_id（同目录）还是独立 cursor；
7. 榜单 ID 清单：App「榜单」频道的一次列表请求（可能有 `/reading/rank/list` 类聚合端点）；
8. 鉴权差异：榜单是否要求登录态/更强的 Argus-Ladon 签名（guoapp 的目录请求匿名可用，榜单可能同样匿名，需验证）。

抓包拿到上述任一证据后，更新本文档 §1.1 与 §8，并为重写版的榜单规格（`.scratch/guo-web/issues/07-rankings-spec.md`）提供端点级输入。

---

## 附：重写版最小协议面清单（直接可实现的结论）

1. **必做端点**：App 侧 4 个（landpage 目录 / video_detail 详情 / video_model 取流 / comment 弹幕）+ Web 侧 4 个（category 兜底 / detail 兜底 / player 取流 / rank 榜单）+ 联想 1 个 + 兜底取流 1 个（可选）。
2. **设备身份**：随机 19 位 device_id/iid 进程内固定；query 设备参数照抄 §5.1；UA 用老版本（70000/Android 12）并可配置。
3. **签名**：普通请求只需 X-Gorgon/X-Khronos/X-SS-Req-Ticket（+X-SS-STUB）；弹幕额外 Comment-Source/Server-Channel/X-Argus/X-Ladon。实现照抄 `provider_hongguo_sign.go`、`provider_hongguo_comment_sign.go`。
4. **取流策略**：Web 优先 → App → 兜底；App 侧 codec 过滤只信 `video_meta.codec_type`；bytevc 档降权不硬 ban。
5. **解密**：Go 只提取密钥（`hongguoContentKey`），解密交给 ffmpeg/mpv（`decryption_key`）；下载存密文+key，合并时 ffmpeg 解密出明文。
6. **分页**：App 目录 offset+session_id（18/页，session 30 分钟有效）；Web/榜单 page 页码（24 与 20 条每页）。
7. **弹幕**：30 秒窗口、count 90、`next_query_danmaku_list_time` 游标续拉。
