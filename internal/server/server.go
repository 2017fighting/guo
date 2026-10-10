// Package server 提供 guo 的 HTTP 服务：/api/v1 REST API + 前端静态资源。
// 无路由依赖（标准库 net/http）；错误统一 JSON {"message","hint"}，人话不说码。
package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"

	"github.com/2017fighting/guo/internal/hongguo"
)

// CatalogSource 目录数据源（*hongguo.Client 实现；接口让 handler 可测）。
type CatalogSource interface {
	CatalogPage(ctx context.Context, q hongguo.CatalogQuery) (*hongguo.CatalogPage, error)
	CatalogFilters(ctx context.Context) (*hongguo.CatalogFilters, error)
}

// Server HTTP 入口。Static 为前端产物根（含 index.html，web/dist），
// embed 接线在容器化工单完成；nil 表示仅 API。
type Server struct {
	Catalog  CatalogSource
	Search   SearchSource
	Settings SettingsStore
	Static   fs.FS
}

// Handler 返回完整路由（API + 静态回落）。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	if s.Static != nil {
		mux.Handle("/", spaHandler(s.Static))
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusNotFound, "前端页面还没有打包进来",
				"先运行 pnpm -C web build 再启动；也可以直接调用 /api/v1/catalog 等接口")
		})
	}
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError 统一错误出口：message 讲人话，hint 给出路或上游细节。
func writeError(w http.ResponseWriter, status int, message, hint string) {
	writeJSON(w, status, map[string]string{"message": message, "hint": hint})
}
