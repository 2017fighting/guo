//go:build !embed

package web

import "io/fs"

// Dist 默认构建（未启用 embed 标签）恒为 nil：serve 回落磁盘 web/dist（开发模式）。
// 详见 embed.go 的包注释。
func Dist() fs.FS { return nil }
