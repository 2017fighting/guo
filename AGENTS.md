# guo

红果短剧 Web 应用：Go 后端 + React/Vite/Tailwind/shadcn 前端，Docker 部署。
功能面：浏览（筛选/排序）、排行榜、搜索、详情、在线播放（弹幕叠加）、下载进 Jellyfin（无加密 + NFO + 图片 + ASS 弹幕字幕）。
规划走 wayfinder 地图（GitHub Issues，`wayfinder:map` 标签）。参考实现只读：`/home/zhao/clone/guoapp`、`/home/zhao/clone/DanmuBridge`。

## Agent skills

### Issue tracker

Issues live in GitHub Issues (this repo) via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default five canonical roles: needs-triage / needs-info / ready-for-agent / ready-for-human / wontfix. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `CONTEXT.md` + `docs/adr/` at the root. See `docs/agents/domain.md`.
