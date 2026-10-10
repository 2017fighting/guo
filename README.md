# guo — 红果短剧下载 · 在线观看 · Jellyfin 入库

Go 后端 + React 前端的红果短剧自托管服务：浏览 / 排行榜 / 搜索 / 详情 / 在线播放（弹幕叠加），一键下载无加密视频 + NFO + 海报 + ASS 弹幕字幕到 Jellyfin 媒体库。

## 部署（Docker）

> 前置：已装 Docker（含 compose 插件）。镜像 `ghcr.io/2017fighting/guo` 由 CI 在 `v*` 标签自动发布。

### 一条命令起服务

```sh
mkdir guo && cd guo
# 下载 docker-compose.yml（或 git clone 本仓库后进入根目录）
docker compose up -d
```

打开 `http://<主机IP>:8080` 即可浏览、搜索、在线播放；下载入口在剧集详情页。

### Jellyfin 联动（可选）

1. 编辑 `docker-compose.yml`：解开 `jellyfin` 服务段和 `GUO_JELLYFIN_URL` / `GUO_JELLYFIN_KEY` 两行注释，`docker compose up -d`。
2. 打开 `http://<主机IP>:8096` 完成 Jellyfin 首次向导（管理员账号即可，媒体库已自动指向共享媒体卷）。
3. Jellyfin 控制台 → API Keys → 新建 Key，填进 compose 的 `GUO_JELLYFIN_KEY`（或启动后在 guo 设置页填 URL + Key，保存时自动验活）。
4. 之后每批下载完成会自动触发 Jellyfin 库刷新；媒体目录结构（`剧名/Season 01/S01E001.mp4` + 同基名 `.nfo` / `.zh.ass` + `poster.jpg`）按 Jellyfin 规范落盘，开箱入库。

> 已有自建 Jellyfin？只需把它挂到 guo 同一媒体目录，再在设置页填它的地址和 API Key。

### 环境变量与卷

| 环境变量 | 默认 | 说明 |
| --- | --- | --- |
| `GUO_ADDR` | `:8080` | HTTP 监听地址 |
| `GUO_MEDIA_DIR` | `/data/media` | 媒体库根（下载产物） |
| `GUO_DB` | `/data/db/guo.db` | SQLite 任务库（断电重启续传） |
| `GUO_CONCURRENCY` | `2` | 并发下载数 1–6（设置页可热调） |
| `GUO_JELLYFIN_URL` | 未设 | Jellyfin 地址（如 `http://jellyfin:8096`；不联动则不设） |
| `GUO_JELLYFIN_KEY` | 未设 | Jellyfin API Key（控制台 → API Keys 创建） |
| `GUO_FFMPEG` | `ffmpeg` | ffmpeg 路径（镜像内已含，一般无需设） |

| 卷 | 用途 |
| --- | --- |
| `/data/media` | 媒体库——与 Jellyfin 共享（guo 挂 `/data/media`，Jellyfin 挂 `/media`，同一卷） |
| `/data/db` | 任务库与设置 |
| `/data/config` | 预留配置位 |

### 说明

- **国内直连**：访问红果源站为直连（无代理转发，v1 未实现；设置页已留出站代理配置位）。拉取 ghcr 镜像慢时可配 Docker 镜像代理，或改用 `build: .` 本地自构建。
- **更新镜像**：`docker compose pull && docker compose up -d`（数据都在卷里，任务与媒体不丢）。
- **自构建**：compose 里注释 `image:`、解开 `build: .`；多阶段构建（node 构建前端 → CGO_ENABLED=0 静态编译 → alpine + ffmpeg 运行层），前端产物 embed 进单二进制，镜像内已含 ffmpeg。

## 本地开发

```sh
go run ./cmd/guo serve            # API :8080（GUO_* 环境变量见 cmd/guo/main.go 头注释）
pnpm -C web install && pnpm -C web dev   # 前端 :5173，/api 代理到 :8080
```

前端构建产物 `web/dist` 默认不入库：`guo serve` 会优先伺服磁盘上的 `web/dist`（开发模式热替换）；单二进制发布用 `go build -tags embed ./cmd/guo`（要求先 `pnpm -C web build`，容器镜像内部即此流程，见 `web/embed.go`）。
