package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// staticFS 是 serve 的前端来源选择：磁盘 web/dist（开发模式）优先，否则编译期 embed。

func TestStaticFSPrefersDiskDist(t *testing.T) {
	disk := t.TempDir()
	if err := os.WriteFile(filepath.Join(disk, "index.html"), []byte("disk build"), 0o644); err != nil {
		t.Fatal(err)
	}
	embedded := &fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("embedded build")}}
	got := staticFS(disk, embedded)
	b, err := fs.ReadFile(got, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "disk build" {
		t.Fatalf("磁盘 dist 应优先于 embed，got %q", b)
	}
}

func TestStaticFSFallsBackToEmbedded(t *testing.T) {
	embedded := &fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("embedded build")}}
	// 目录存在但没有 index.html（未跑 pnpm build）→ 视为无磁盘构建
	if got := staticFS(t.TempDir(), embedded); got != fs.FS(embedded) {
		t.Fatalf("无磁盘构建时应原样返回 embed FS，got %#v", got)
	}
}

func TestStaticFSNilWhenNoDistAnywhere(t *testing.T) {
	// 默认构建（无 embed 标签）web.Dist() 为 nil：两处都缺 → 不伺服前端
	if got := staticFS(t.TempDir(), nil); got != nil {
		t.Fatalf("应返回 nil（不伺服静态），got %#v", got)
	}
}
