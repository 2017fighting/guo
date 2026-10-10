package server

import (
	"net/http"

	"github.com/2017fighting/guo/internal/hongguo"
)

// handleCatalog GET /api/v1/catalog?genre=&offset=&session_id=
// 返回规范化条目 + 下一页游标；genre 回显（缺省 all）。
func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	if s.Catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "目录数据源未配置", "请检查服务启动日志")
		return
	}
	query := r.URL.Query()
	genre := query.Get("genre")
	if genre == "" {
		genre = hongguo.CatalogGenreAll
	}
	if genre != hongguo.CatalogGenreAll && genre != hongguo.CatalogGenreShortPlay &&
		genre != hongguo.CatalogGenreComic && genre != hongguo.CatalogGenreAI {
		writeError(w, http.StatusBadRequest, "请求参数不对",
			"genre 只支持 all / short_play / comic_series / ai_series")
		return
	}
	offset, ok := parseOffset(w, query.Get("offset"))
	if !ok {
		return
	}
	page, err := s.Catalog.CatalogPage(r.Context(), hongguo.CatalogQuery{
		Genre: genre, Offset: offset, SessionID: query.Get("session_id"),
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "目录数据加载失败，请稍后重试", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"genre":      genre,
		"items":      page.Items,
		"offset":     page.Offset,
		"session_id": page.SessionID,
		"has_more":   page.HasMore,
	})
}

// handleCatalogFilters GET /api/v1/catalog/filters —— 浏览页筛选面板枚举。
func (s *Server) handleCatalogFilters(w http.ResponseWriter, r *http.Request) {
	if s.Catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "目录数据源未配置", "请检查服务启动日志")
		return
	}
	filters, err := s.Catalog.CatalogFilters(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "筛选项暂时拉不到，请稍后重试", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, filters)
}
