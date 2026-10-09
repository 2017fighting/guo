# 弹幕转 ASS 字幕调研

Status: resolved
Type: research

## Question

调研弹幕→ASS 字幕的转换设计，写成 `docs/research/danmaku-ass.md`：

1. DanmuBridge（`/home/zhao/clone/DanmuBridge`，只读）的转换管线：弹幕格式解析、ASS 头部样式（字体/字号/颜色/边框）、滚动/顶部/底部弹幕的 ASS 事件写法、时间轴处理、防碰撞/布局策略（若有）
2. 红果弹幕数据结构（guoapp `native/core/app_danmaku.go` 及 provider 侧）：字段、时间基准、弹幕类型标记
3. 两者对接：红果弹幕字段 → ASS 事件的映射表，以及用 Go 实现该转换器的要点与坑

## Answer

调研完成，成果：`docs/research/danmaku-ass.md`（168 行，3 节，源码逐行核对）。

要点：
- **DanmuBridge 自身不生成 ASS**——它运行时子进程调用 `m13253/danmaku2ass`（**GPL-3.0**，参数 `-s 1920x1080 -fn 黑体 -fs 30 -dm 8 -ds 8`）；ASS 头部样式、`\move`/`\an` 事件写法、逐像素行占用防碰撞（追尾速度模型 `(W+w)/dm`）全在 danmaku2ass。
- 红果弹幕（guoapp `ui_playback_danmaku.go`，`/novel/commentapi/comment/list/{videoID}/v1/` 30s 窗口分页）条目**只有 ID/文本/毫秒偏移三字段，无类型/颜色/字号标记**——对接时全部映射为右→左滚动弹幕即可。
- 文档含完整字段映射表 + 12 条 Go 实现要点（rune 估宽、ASS 颜色 BGR 倒序、UTF-8 BOM+CRLF、厘秒四舍五入、`{}` 转义防标签注入、**GPL 传染风险**、每页 90 条上限等）。GPL 许可证取舍已挂入「下载管线规格对谈」。
