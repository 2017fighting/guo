package server

// /api/v1/drama/{seriesID}：详情 + 分集（App→Web 回落由源客户端实现）。

import (
	"context"
	"net/http"
	"regexp"

	"github.com/2017fighting/guo/internal/pipeline"
)

// DramaSource 详情数据源（*hongguo.Client 实现；接口让 handler 可测）。
type DramaSource interface {
	Detail(ctx context.Context, seriesID string) (*pipeline.DramaMeta, error)
}

var seriesIDPattern = regexp.MustCompile(`^[0-9]{1,32}$`)

type dramaEpisodePayload struct {
	Index int    `json:"index"`
	VID   string `json:"vid"`
}

type dramaDetailPayload struct {
	SeriesID string                `json:"series_id"`
	Title    string                `json:"title"`
	Year     string                `json:"year"`
	Plot     string                `json:"plot"`
	Cover    string                `json:"cover"`
	Fanart   string                `json:"fanart"`
	Genres   []string              `json:"genres"`
	Episodes []dramaEpisodePayload `json:"episodes"`
}

// handleDrama GET /api/v1/drama/{seriesID}
func (s *Server) handleDrama(w http.ResponseWriter, r *http.Request) {
	if s.Drama == nil {
		writeError(w, http.StatusServiceUnavailable, "详情数据源未配置", "请检查服务启动日志")
		return
	}
	seriesID := r.PathValue("seriesID")
	if !seriesIDPattern.MatchString(seriesID) {
		writeError(w, http.StatusBadRequest, "剧 ID 不对", "series_id 应为 1–32 位数字（红剧集 ID）")
		return
	}
	meta, err := s.Drama.Detail(r.Context(), seriesID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "详情加载失败，请稍后重试", err.Error())
		return
	}
	payload := dramaDetailPayload{
		SeriesID: meta.SeriesID, Title: meta.Title, Year: meta.Year, Plot: meta.Plot,
		Cover: meta.CoverURL, Fanart: meta.FanartURL, Genres: meta.Genres,
		Episodes: make([]dramaEpisodePayload, 0, len(meta.Episodes)),
	}
	for _, ep := range meta.Episodes {
		payload.Episodes = append(payload.Episodes, dramaEpisodePayload{Index: ep.Index, VID: ep.VID})
	}
	if payload.Genres == nil {
		payload.Genres = []string{}
	}
	writeJSON(w, http.StatusOK, payload)
}
