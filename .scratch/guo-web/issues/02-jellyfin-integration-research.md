# Jellyfin 集成面调研

Status: claimed
Type: research

## Question

调研 Jellyfin 集成面，写成 `docs/research/jellyfin-integration.md`（结论引用 Jellyfin 官方文档与 guoapp 现成实现）：

1. 短剧内容在 Jellyfin 中的库结构最佳实践（目录布局、剧名/SxxExx/季命名、特典处理）
2. `tvshow.nfo` 与分集 NFO 的字段规范（对照 guoapp `lib/media_library.dart` 的 emby 生成器与 Jellyfin 官方 NFO 插件文档）
3. poster/fanart/still 图片命名与尺寸建议
4. 外挂 `.ass` 字幕的命名（语言标记）与 Jellyfin 默认外挂行为
5. 下载完成后触发库刷新的最小 API 面（端点、所需权限、按目录刷新 vs 全库刷新、API Key 配置方式）
