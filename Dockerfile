# syntax=docker/dockerfile:1
# guo 多阶段镜像：前端构建 → Go 静态编译（embed 前端）→ alpine 运行层（含 ffmpeg）。
# spec.md §11；运行层选 alpine + apk ffmpeg（比 linuxserver/ffmpeg 基更小、依赖可控）。

# ---- 阶段 1：前端（Vite 产物 web/dist）----
FROM node:22-alpine AS web
# pnpm 与仓库锁文件版本对齐（web/pnpm-lock.yaml lockfileVersion 9.0）
RUN npm install -g pnpm@12.10.1
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

# ---- 阶段 2：Go 静态编译（CGO_ENABLED=0，纯静态；-tags embed 打包前端）----
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -tags embed -ldflags="-s -w" -o /out/guo ./cmd/guo

# ---- 阶段 3：运行层 ----
FROM alpine:3.22
# ffmpeg：下载管线解密合并依赖（-decryption_key，见 internal/pipeline）；
# tini：转发信号给 guo（JobRunner 收 SIGTERM 后等在跑任务到分集边界退出）。
RUN apk add --no-cache ca-certificates ffmpeg tzdata tini \
	&& addgroup -S guo && adduser -S -G guo -h /data guo \
	&& mkdir -p /data/media /data/db /data/config \
	&& chown -R guo:guo /data
COPY --from=build /out/guo /usr/local/bin/guo

# GUO_* 运行配置（docker-compose.yml 可逐项覆盖）：
#   GUO_ADDR            监听地址（默认 :8080）
#   GUO_MEDIA_DIR       媒体库根（下载产物：视频/NFO/海报/ASS 弹幕字幕）
#   GUO_DB              SQLite 任务库
#   GUO_CONCURRENCY     并发下载数 1-6（默认 2）
#   GUO_JELLYFIN_URL    Jellyfin 地址（未设=不联动；如 http://jellyfin:8096）
#   GUO_JELLYFIN_KEY    Jellyfin API Key（控制台 → API Keys 创建）
#   GUO_FFMPEG          ffmpeg 路径（默认 PATH 上的 ffmpeg，镜像内已含）
ENV GUO_ADDR=:8080 \
	GUO_MEDIA_DIR=/data/media \
	GUO_DB=/data/db/guo.db \
	GUO_CONCURRENCY=2

USER guo
WORKDIR /data
EXPOSE 8080
# /data/config 为预留配置位（v1 设置存 SQLite，见 spec §13 出站代理预留）
VOLUME ["/data/media", "/data/db", "/data/config"]
ENTRYPOINT ["/sbin/tini", "--"]
CMD ["guo", "serve"]
