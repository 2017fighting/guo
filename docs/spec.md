# guo 实现规格（v1 · wayfinder 地图收官汇编）

> 决策来源：GitHub Issues #2–#9（决议评论为权威记录）+ `docs/research/`（协议/Jellyfin/弹幕调研）+ `mockups/`（交互拍板）。本文是开工蓝图；各节冲突时以对应 issue 决议为准。

## 1. 概述

红果短剧 Web 应用：独立 Docker 化服务（Go 后端 + React 前端），浏览/排行榜/搜索/详情/在线播放（弹幕叠加）/下载进 Jellyfin（无加密 MP4 + NFO + 图片 + ASS 弹幕字幕，完成后自动触发库刷新）。首版仅红果源；无认证；单用户家用。

**已实现现状**（截至本规格成文）：下载管线全链路（`internal/{layout,nfo,ass,jellyfin,store,pipeline,hongguo}` + `cmd/guo` CLI）已上线并真机验证（76 集整剧 → Jellyfin 全字段正确入库）；测试 7 包全绿（含 -race）。本规格的增量 = Web API 层 + 前端 + 排行榜 + 容器化。

## 2. 技术栈

- 后端 Go（单二进制，`modernc.org/sqlite` 纯 CGO-free，容器内置 ffmpeg）
- 前端 React + Vite + Tailwind CSS + 官方 shadcn/ui（令牌契约 `ui-contract.tokens.json`）
- 播放 hls.js（MP4 直链场景直接 `<video>`）；弹幕自绘 Canvas 叠加（数据复用下载导出源）
- 分发：Dockerfile + compose，CI 发布 ghcr.io

## 3. 架构与模块

```
cmd/guo            入口（flag: -addr ; env: GUO_*）
internal/
  hongguo/         红果源客户端（已实现：App API + Web SSR + 兜底 + 弹幕 + 签名移植）
    rankings/      新增：api3 榜单客户端（轻 Gorgon + 设备参数/UA 73970，游标分页，10min 内存+磁盘缓存）
  pipeline/        下载引擎（已实现；新增 JobRunner 常驻 goroutine 化：队列事件驱动，替代 CLI 单次 Run）
  store/           SQLite(WAL)：jobs/episodes/episode_meta/settings（已实现；新增 rankings 缓存表）
  layout|nfo|ass|jellyfin/   （已实现）
  server/          新增：HTTP API + 静态资源伺服（embed 前端产物）
web/               新增：React 前端
```

- 引擎常驻：`Run` 改事件驱动（任务创建/恢复/暂停即唤醒；无任务时静默），并发语义不变（全局 2 剧、剧内串行、分集自动重试≤3）。
- 出站代理：配置项预留（`settings` 表 + env），首版不实现直连链路（部署环境国内直连）。

## 4. 后端 API 面（REST，`/api/v1`，无认证）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | /catalog | 目录 feed：genre（all/short_play/comic_series/ai_series）+ 游标（offset/session_id，18 条/页）+ 客户端排序过滤（同 guoapp 语义：热度/上线/播放量/名称/季号 + 完结状态）；genre=all 为三分类并发聚合，单页最多 54 条（3×18），游标各自推进 |
| GET | /catalog/filters | 筛选面板枚举（题材/状态/上新时段；来自 need_selector_panel 或抓包枚举） |
| GET | /rankings?list=&offset= | 8 榜单（见 §10）；游标分页；响应含更新时间 |
| GET | /search?q= | 官网搜索页 + 名称索引双通道合并（server 端 300ms 防抖由前端做） |
| GET | /search/suggest?q= | 联想（≤10 条，白名单 word_type） |
| GET | /drama/{seriesID} | 详情+分集（App→Web 回落） |
| GET | /drama/{seriesID}/episodes/{vid}/stream | 取流三级回退（Web→App→兜底），返回代理播放地址 + 画质档 + 时长 + 画面方向 |
| GET | /stream/* | 媒体流代理（Range 转发、Referer 策略、403/410 自动重取换址——复用 pipeline 下载逻辑） |
| GET | /drama/{seriesID}/episodes/{vid}/danmaku?from=&duration= | 弹幕窗口（30s 窗口游标，前端边播边拉；duration=集时长 ms，窗口语义需要，服务端用于尾窗判断） |
| POST | /downloads | 建任务 {seriesID, quality, episodes[]}（幂等；done 任务补集自动重入队） |
| GET | /downloads | 队列列表（剧卡片聚合：状态/进度/速度/当前分集） |
| GET | /downloads/{jobID}/episodes | 分集明细 |
| POST | /downloads/{jobID}/pause·resume·retry | 任务控制 |
| DELETE | /downloads/{jobID}?keepVideo= | 删除（可选保留视频） |
| GET/PUT | /settings | ass_export / jellyfin(url+key，PUT 时验活) / concurrency 等 |

事件推送：SSE `/api/v1/events`（队列进度变更推送；轮询兜底）。错误口径：JSON `{message, hint}`，人话不说码。

## 5. 数据模型（SQLite，已实现 jobs/episodes/episode_meta/settings）

新增 `rankings_cache(list TEXT, offset INTEGER, payload TEXT, updated_at INTEGER, PRIMARY KEY(list, offset))`——磁盘 stale 兜底（当日旧榜可用，接口失败时返回并标注 updated_at）。

## 6. 红果协议面

权威细节见 `docs/research/hongguo-protocol.md`（8 节）+ `#5/#8` 决议：
- 普通请求：轻量 Gorgon（X-Gorgon/X-Khronos/X-SS-Req-Ticket[+STUB]），设备参数 + 老 UA
- 榜单：`api3-normal-sinfonlinec`（设备参数/UA 73970 取自抓包真机）`/reading/bookapi/plan` + `/reading/bookapi/bookmall/cell/change/v`，游标 next_offset+session_id（10 条/页），条目 `video_data[]{title,cover,video_desc,series_id,vertical,recommend_info.rank}`
- 取流：Web 播放器页优先（h264 无加密）→ App API → djapi 兜底（v2 信封 + spade_a 密钥）
- CENC：Go 提取密钥，解密在 ffmpeg demux 层（`-decryption_key`）
- 弹幕：评论接口全套签名（Argus/Ladon/SM3），30s 窗口
- CDN 直链 30 分钟过期：流代理 403/410 时重取（已实现）

## 7. 前端页面清单（交互以 `mockups/` 拍板为准 + #6 决议）

1. **浏览**：类型 Tabs（4）→ 排序下拉 → 可展开筛选；海报墙（2:3 卡：标题/集数/状态/热度，VIP 角标）；无限加载+底部兜底
2. **搜索**：联想下拉 + 最近搜索 chips + 结果海报墙
3. **详情**：资料卡 + 可展开简介；主行动「立即播放」；下载弹层（画质 3 档默认最高 + 分集多选分组 + 体积预估——源站详情接口无分集体积字段，降级为仅显示分集数/画质档，后续源站若暴露体积字段再启用）
4. **播放**：画面比例自适应（16:9 主形态/9:16 竖屏窄栏）；控制条含弹幕开关/画质/线路/下一集；键盘上下切集；弹幕 Canvas 叠加（样式对齐 ASS 导出口径）
5. **下载队列**：任务=剧卡片三态（下载中/完成/失败含原因出路）；确定值进度+分集明细折叠（✓/↓/数字）；暂停/继续/重试/更新本剧/删除确认（保留视频可选）
6. **排行榜**（新增，模式同浏览页）：横向 8 榜 Tab；条目=海报卡+rank 序号角标；数据更新时间标注
- 全局：顶部导航（浏览/排行榜/下载队列+搜索+主题切换）；移动底部 Tab 4 项；深浅双主题（shadcn 令牌）；无认证

## 8. 下载管线（已实现，#7 决议）

目录：`MEDIA_DIR/剧名 (年份)/{tvshow.nfo,poster.jpg[,fanart.jpg]} + Season 01/S01E001.{mp4,nfo,zh.ass}`；剧集级产物任务创建即写。任务=剧+选集；全局并发 2（1–6 可配）；MP4 Range 断点续传；分集失败自动重试≤3；ffmpeg `-decryption_key` + `-c copy` 合并解密一步完成；弹幕→ASS 清洁实现（ADR-0001）；SQLite 持久化；整剧完成触发 `POST /Library/Refresh`（`Authorization: MediaBrowser Token`）；ASS 默认导出（settings 可关）；完成通知不做（页内状态+刷新即所得）。

## 9. Jellyfin 集成

配置 URL+API Key（PUT settings 时 `GET /System/Info` 验活）；库结构/NFO/图片/字幕命名按 `docs/research/jellyfin-integration.md`（真机 76/76 验证）；测试实例 `scripts/jellyfin-test.sh`。

## 10. 排行榜（#8 决议）

8 榜：全站 热门/热播/口碑/上新/必看 + 真人热播/漫剧热榜/AI 热门。独立 Tab（导航占位转正，移动端第 4 项）。缓存：内存 10 分钟 + 磁盘当日 stale 兜底。不留旧官网 4 榜降级；失败重试+报错。

## 11. 容器化与发布

- 多阶段 Dockerfile：`node` 构建前端 → `golang`（纯静态 CGO_ENABLED=0）→ 运行层含 ffmpeg（如 `linuxserver/ffmpeg` 基或 alpine+ffmpeg 包）
- `docker-compose.yml`：guo 服务（env：GUO_MEDIA_DIR/GUO_DB/GUO_JELLYFIN_*/GUO_CONCURRENCY/GUO_ADDR；volumes：media、db、config）+ 注释化的 Jellyfin 联动示例
- **CI 发布 ghcr.io**（GitHub Actions：tag `v*` 构建多架构 linux/amd64[,arm64] 推 `ghcr.io/2017fighting/guo`）
- 镜像安全更新跟进纳入日常维护

## 12. 里程碑

| 里程碑 | 内容 | 状态 |
|---|---|---|
| M1 下载管线 | internal/* + CLI + 真机验证 | ✅ 完成 |
| M2 Web 骨架 | server 包（API 全量）+ JobRunner 常驻化 + SSE | ⬜ |
| M3 前端五页 | React 骨架 + 浏览/搜索/详情/队列（对接 API） | ⬜ |
| M4 播放与榜单 | 播放页（流代理+hls/video+弹幕 Canvas）+ 排行榜 8 榜页 | ⬜ |
| M5 容器化收官 | Dockerfile/compose/ghcr CI + 部署文档 + 端到端验收 | ⬜ |

## 13. 界外与后续工作（不做于本规格）

Jellyfin 插件、多用户/认证、观看进度记录、追剧体系、全站源、解除源站 VIP、Web-SSR 之外的站源扩展位（`pipeline.Source` 接口即预留）、出站代理实现（配置位已留）、下载通知（若将来要：webhook/telegram 再议）。
