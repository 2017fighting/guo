package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/2017fighting/guo/internal/hongguo"
)

// SearchSource 搜索/联想数据源（*hongguo.Client 实现；接口让 handler 可测）。
// 防抖由前端负责，服务端不做。
type SearchSource interface {
	Search(ctx context.Context, query string) (*hongguo.SearchResult, error)
	Suggest(ctx context.Context, query string) ([]hongguo.SuggestItem, error)
}

// handleSearch GET /api/v1/search?q= —— 双通道合并结果。
// 空 q 返回空结果而非报错；关键词校验失败 400；上游全挂 502。
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if s.Search == nil {
		writeError(w, http.StatusServiceUnavailable, "搜索数据源未配置", "请检查服务启动日志")
		return
	}
	q := r.URL.Query().Get("q")
	if strings.TrimSpace(q) == "" {
		writeJSON(w, http.StatusOK, &hongguo.SearchResult{
			Items:    []hongguo.CatalogItem{},
			Warnings: []string{},
		})
		return
	}
	result, err := s.Search.Search(r.Context(), q)
	if err != nil {
		writeSearchError(w, err, "搜索暂时不可用，请稍后重试")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleSearchSuggest GET /api/v1/search/suggest?q= —— 联想（≤10 条）。
func (s *Server) handleSearchSuggest(w http.ResponseWriter, r *http.Request) {
	if s.Search == nil {
		writeError(w, http.StatusServiceUnavailable, "搜索数据源未配置", "请检查服务启动日志")
		return
	}
	q := r.URL.Query().Get("q")
	if strings.TrimSpace(q) == "" {
		writeJSON(w, http.StatusOK, map[string]any{"items": []hongguo.SuggestItem{}})
		return
	}
	items, err := s.Search.Suggest(r.Context(), q)
	if err != nil {
		writeSearchError(w, err, "联想暂时不可用，请稍后重试")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// writeSearchError 关键词问题 → 400（人话 + 出路）；其余 → 502 带上游细节。
func writeSearchError(w http.ResponseWriter, err error, upstreamMessage string) {
	if errors.Is(err, hongguo.ErrKeywordInvalid) {
		writeError(w, http.StatusBadRequest, "关键词不合适",
			"请用 1-80 个字符的剧名/演员/题材关键词，去掉换行等控制字符再试")
		return
	}
	writeError(w, http.StatusBadGateway, upstreamMessage, err.Error())
}
