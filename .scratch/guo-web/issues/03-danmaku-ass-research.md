# 弹幕转 ASS 字幕调研

Status: claimed
Type: research

## Question

调研弹幕→ASS 字幕的转换设计，写成 `docs/research/danmaku-ass.md`：

1. DanmuBridge（`/home/zhao/clone/DanmuBridge`，只读）的转换管线：弹幕格式解析、ASS 头部样式（字体/字号/颜色/边框）、滚动/顶部/底部弹幕的 ASS 事件写法、时间轴处理、防碰撞/布局策略（若有）
2. 红果弹幕数据结构（guoapp `native/core/app_danmaku.go` 及 provider 侧）：字段、时间基准、弹幕类型标记
3. 两者对接：红果弹幕字段 → ASS 事件的映射表，以及用 Go 实现该转换器的要点与坑
