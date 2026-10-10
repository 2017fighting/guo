package server

import (
	"io/fs"
	"net/http"
	"net/url"
	"strings"
)

// spaHandler 静态资源伺服：文件存在直接回；未命中且非 /api 路径回落 index.html
// （前端客户端路由需要）。/api 下的未知路径不回落，保持 JSON 404。
func spaHandler(root fs.FS) http.Handler {
	files := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusNotFound, "接口不存在", "请检查请求路径，或查看 /api/v1/catalog")
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(root, name); err != nil {
			fallback := r.Clone(r.Context())
			fallback.URL = &url.URL{Path: "/", RawQuery: r.URL.RawQuery}
			files.ServeHTTP(w, fallback)
			return
		}
		files.ServeHTTP(w, r)
	})
}
