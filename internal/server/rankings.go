package server

import (
	"net/http"
	"strconv"

	"github.com/2017fighting/guo/internal/hongguo/rankings"
)

// handleRankings GET /api/v1/rankings?list=&offset=&session_id=
// 返回 8 榜单页（items + 游标 + has_more + updated_at）。
func (s *Server) handleRankings(w http.ResponseWriter, r *http.Request) {
	if s.Rankings == nil {
		writeError(w, http.StatusServiceUnavailable, "榜单数据源未配置", "请检查服务启动日志")
		return
	}
	query := r.URL.Query()
	list := query.Get("list")
	if _, ok := rankings.BoardByID(list); !ok {
		writeError(w, http.StatusBadRequest, "请求参数不对",
			"list 请从 8 榜中选择，如 ranklist_hot_sc（热门）/ ranklist_hot_play_sc（热播）/ ranklist_prestige（口碑）/ ranklist_new_rank_sc（上新）/ ranklist_must_watch（必看）/ human_hot_play（真人热播）/ comic_series_hot_rank（漫剧热榜）/ ai_playlet_hot_sc（AI热门）")
		return
	}
	offset := 0
	if raw := query.Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "请求参数不对", "offset 需是非负整数，请从上一页响应里取游标")
			return
		}
		offset = n
	}
	page, err := s.Rankings.Page(r.Context(), list, offset, query.Get("session_id"))
	if err != nil {
		writeError(w, http.StatusBadGateway, "榜单数据加载失败，请稍后重试", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, page)
}
