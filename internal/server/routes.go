package server

import "net/http"

// routes API 路由集中注册（全部 /api/v1）。后续 lane（搜索/排行榜/下载/设置/流）
// 在此各追加一行，把自己的 handler 放独立文件，保持跨分支 diff 最小。
func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/catalog", s.handleCatalog)
	mux.HandleFunc("GET /api/v1/catalog/filters", s.handleCatalogFilters)
}
