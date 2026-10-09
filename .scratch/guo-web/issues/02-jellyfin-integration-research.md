# Jellyfin 集成面调研

Status: resolved
Type: research

## Question

调研 Jellyfin 集成面，写成 `docs/research/jellyfin-integration.md`（结论引用 Jellyfin 官方文档与 guoapp 现成实现）：

1. 短剧内容在 Jellyfin 中的库结构最佳实践（目录布局、剧名/SxxExx/季命名、特典处理）
2. `tvshow.nfo` 与分集 NFO 的字段规范（对照 guoapp `lib/media_library.dart` 的 emby 生成器与 Jellyfin 官方 NFO 插件文档）
3. poster/fanart/still 图片命名与尺寸建议
4. 外挂 `.ass` 字幕的命名（语言标记）与 Jellyfin 默认外挂行为
5. 下载完成后触发库刷新的最小 API 面（端点、所需权限、按目录刷新 vs 全库刷新、API Key 配置方式）

## Answer

调研完成，成果：`docs/research/jellyfin-integration.md`（221 行，5 节 + 附录：矛盾与过时信息记录、缺失证据记录；外部结论按 10.10/10.11/master 三版源码交叉核对并给 URL/源码引用，guoapp 对照给文件/函数名）。

要点：
- 库结构定型：`剧名/tvshow.nfo + poster.jpg + Season 01/S01E001.mkv + .nfo + S01E001.zh.ass（+可选 thumb）`；季目录必须写全 `Season` 且补零；红果短剧不在 TMDb/TVDb 覆盖内，剧目录**只用剧名（可加年份），不伪造 provider id**。
- 外挂弹幕字幕命名 `S01E001.zh.ass`（语言标记）。
- 刷新 API 最小面已按 10.10/10.11 源码核对；社区旧参数（`Recursive`、`X-Emby-Token`、`?api_key=`）标注了过时风险。
- guoapp 的 `embyShowNfo`/`exportJobs` 层级命名全部符合官方规范，思路可沿用。
