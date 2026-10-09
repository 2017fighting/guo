# 弹幕→ASS 转换器清洁重写，不引入 danmaku2ass

调研（`docs/research/danmaku-ass.md`）发现参考项目 DanmuBridge 实际是子进程调用 danmaku2ass（GPL-3.0）完成 ASS 生成；而红果弹幕条目只有 ID/文本/毫秒偏移三个字段、全部映射为右→左滚动弹幕，用不到 danmaku2ass 的完整能力（多类型弹幕、复杂防碰撞）。为避免 GPL-3.0 传染本仓库，决定用 Go 清洁重写一个仅覆盖滚动弹幕的精简转换器（简化行布局），不复制 danmaku2ass 代码、不以子进程方式分发它。

## Considered Options

- 子进程调用 danmaku2ass：省开发量，但聚合分发引入 GPL 义务 + 容器内多一套 Python 运行时依赖。
- 清洁重写（选定）：约两百行内可完成，许可证干净，实现依据 `docs/research/danmaku-ass.md` 中的公开格式要点（ASS 头样式、`\move` 事件、行占用模型）独立编写。
