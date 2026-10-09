# 下载管线规格对谈

Status: open
Type: grilling
Blocked by: 01, 02, 03

## Question

对谈定下载管线完整规格：

- 输出目录结构与命名（Jellyfin 短剧库；依据 `docs/research/jellyfin-integration.md`）
- 画质选择与默认档（依据 `docs/research/hongguo-protocol.md` 的画质档位）
- 分集/整剧批量下载
- 并发/暂停/继续/重试/删除语义
- ffmpeg 合并与解密在链路中的落位
- NFO + 图片生成的时机与字段
- ASS 字幕导出（默认开/关、语言标记；依据 `docs/research/danmaku-ass.md`；含 **danmaku2ass GPL-3.0 许可证取舍**：算法重实现 vs 子进程调用）
- 完成回调触发 Jellyfin 刷新
- 失败与断点处理
- 下载元数据持久化选型（SQLite/文件）——本图雾区该项在此毕业
