<!-- 已迁移至 GitHub Issues：地图 = https://github.com/2017fighting/guo/issues/1 。本目录为只读存档，勿再更新。 -->
# 红果 Web 版地图（浏览·搜索·播放·下载进 Jellyfin）

## Destination

锁定一套可直接开工的实现规格（决策+规格成文即收图，实现另起开发会话）：独立 Docker 化 Web 应用——Go 后端 + React/Vite/Tailwind/官方 shadcn.ui 前端，新仓库 `guo` 纯重写，首版仅红果源——覆盖：浏览（筛选/排序）、排行榜、搜索、详情、在线播放（代理原流+弹幕叠加）、下载队列（产出 Jellyfin 可直接导入的无加密视频 + NFO + 图片 + ASS 弹幕字幕，完成后自动触发 Jellyfin 刷新）。

## Notes

- 参考仓库（**只读，不修改**）：
  - `/home/zhao/clone/guoapp`：参考实现。Go 核心 `native/core/`（约 2.4 万行：站源、下载、弹幕、排行榜）；Emby/Jellyfin NFO 生成 `lib/media_library.dart`、`lib/media_exports.dart`；弹幕 `native/core/app_danmaku.go`。
  - `/home/zhao/clone/DanmuBridge`：弹幕→ASS 字幕参考（B 站弹幕→.ass→拷贝进 Jellyfin 目录）。
- 栈级决策已锁定（写规格时直接引用，不必重开）：
  - 独立 Web 应用 + Jellyfin 联动；**不做** Jellyfin 插件
  - 新仓库纯重写，guoapp 仅作参考实现
  - 首版仅红果源
  - 后端 Go（单二进制 + 内嵌前端静态资源，容器内置 ffmpeg）
  - 前端 React + Vite + Tailwind CSS + 官方 shadcn/ui
  - 在线播放：后端代理原流 + hls.js，不转码
  - 在线播放叠加弹幕（与下载导出字幕共用弹幕数据源）
  - Web 端不记录观看进度/追剧
  - 无认证
  - 下载完成后自动触发 Jellyfin 库刷新（可选配置 Jellyfin URL + API Key）
  - 部署环境国内直连；出站代理仅预留配置位，首版不实现
- 排行榜：等用户抓包官方红果 App 接口后再定规格（见「官方红果排行榜抓包」票）。
- 会话应调用技能：grilling、domain-modeling；原型票用 frontend-mockup-loop（system: shadcn）；调研票用 research。
- 调研成果存放约定：`docs/research/`（单文件、结论标注出处）。

## Decisions so far

- [红果源协议考古](issues/01-hongguo-protocol-research.md)：双协议面（官网 SSR + 字节 App API/Gorgon 签名）+ 三级取流回退；CENC 密钥 Go 层提取、解密在 ffmpeg demux 层——详见 `docs/research/hongguo-protocol.md`
- [Jellyfin 集成面调研](issues/02-jellyfin-integration-research.md)：库结构/NFO/图片/外挂字幕命名规范与刷新 API 最小面，按 10.10/10.11 源码核对——详见 `docs/research/jellyfin-integration.md`
- [弹幕转 ASS 字幕调研](issues/03-danmaku-ass-research.md)：转换核心是 danmaku2ass（GPL-3.0）；红果弹幕仅 ID/文本/毫秒偏移三字段，全部映射滚动弹幕——详见 `docs/research/danmaku-ass.md`

## Not yet specified

- 数据持久化选型（下载队列/配置：SQLite 还是文件）——预计随「下载管线规格对谈」或「实现规格汇编收图」毕业
- 下载完成通知（要不要、渠道）——随下载管线或汇编票毕业
- Docker 镜像分发方式（compose 文档 / ghcr 发布）——随汇编票毕业
- 多用户/权限（当前无认证；若将来暴露公网再议）
- 后续站源（韩小圈/MacCMS 系等）的架构预留程度——随汇编票毕业

## Out of scope

- Jellyfin 插件形态：已评估并否决（Go 核心需全部重写为 C#、无官方下载 UI 入口、Jellyfin 10.11→12 插件 API 刚大改）
- 修改 guoapp / DanmuBridge 本体，或在 guoapp 仓库内实现 web 目标
- 全站源支持（真果鉴 `--all-sources` 的其余站源）
- 解除源站 VIP / 授权限制
- 实现与编码本身（收图后另起开发会话执行）
