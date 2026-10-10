package server

import "net/http"

// routes API 路由集中注册（全部 /api/v1）。后续 lane（搜索/排行榜/下载/设置/流）
// 在此各追加一行，把自己的 handler 放独立文件，保持跨分支 diff 最小。
func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/catalog", s.handleCatalog)
	mux.HandleFunc("GET /api/v1/catalog/filters", s.handleCatalogFilters)

	// 详情与下载队列（#11）
	mux.HandleFunc("GET /api/v1/drama/{seriesID}", s.handleDrama)
	mux.HandleFunc("GET /api/v1/downloads", s.handleDownloadsList)
	mux.HandleFunc("POST /api/v1/downloads", s.handleDownloadsCreate)
	mux.HandleFunc("GET /api/v1/downloads/{jobID}/episodes", s.handleDownloadEpisodes)
	mux.HandleFunc("POST /api/v1/downloads/{jobID}/pause", s.handleDownloadControl("pause"))
	mux.HandleFunc("POST /api/v1/downloads/{jobID}/resume", s.handleDownloadControl("resume"))
	mux.HandleFunc("POST /api/v1/downloads/{jobID}/retry", s.handleDownloadControl("retry"))
	mux.HandleFunc("DELETE /api/v1/downloads/{jobID}", s.handleDownloadDelete)
	mux.HandleFunc("GET /api/v1/events", s.handleEvents)

	mux.HandleFunc("GET /api/v1/settings", s.handleSettingsGet)
	mux.HandleFunc("PUT /api/v1/settings", s.handleSettingsPut)
}
