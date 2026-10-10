//go:build embed

// Package web 前端 embed（容器化接线，#16）。
//
// 仅在 `go build -tags embed` 时生效：编译期要求 web/dist 存在（先跑
// `pnpm -C web build`，或容器内由 Dockerfile 阶段 1 构建后拷入）。
// 默认构建不依赖 web/dist——go vet / go test 无需先构建前端；
// serve 无磁盘 web/dist 且未启用本标签时不伺服静态资源。
package web

import (
	"embed"
	"io/fs"
)

// all:dist 含点文件（如 vite 产物内的 .vite 目录），保证 embed 完整。
//
//go:embed all:dist
var distFS embed.FS

// Dist 返回编译期 embed 的前端产物根（即 web/dist 内容）。
// 未用 -tags embed 构建时（embed_stub.go）返回 nil。
func Dist() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("guo/web: embed dist 展开失败: " + err.Error()) // all:dist 保证存在，不可达
	}
	return sub
}
