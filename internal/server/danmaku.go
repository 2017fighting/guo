package server

// /api/v1/drama/{seriesID}/episodes/{vid}/danmaku：弹幕单窗口（30s 游标，
// 播放页边播边拉）。from 由前端对齐 30s 边界；窗口起点透传上游，
// 服务端按上游 next 游标推进（next_ms 即下一窗 from）。

import (
	"context"
	"net/http"
	"strconv"

	"github.com/2017fighting/guo/internal/ass"
)

// DanmakuSource 弹幕数据源（*hongguo.Client 实现；接口让 handler 可测）。
type DanmakuSource interface {
	DanmakuWindow(ctx context.Context, seriesID, vid string, startMS, durationMS int64) ([]ass.Comment, int64, error)
}

type danmakuItem struct {
	ID       string `json:"id,omitempty"`
	OffsetMS int64  `json:"offset_ms"`
	Text     string `json:"text"`
}

type danmakuPayload struct {
	Items  []danmakuItem `json:"items"`
	NextMS int64         `json:"next_ms"`
}

// handleDanmaku GET /api/v1/drama/{seriesID}/episodes/{vid}/danmaku?from=MS&duration=MS
func (s *Server) handleDanmaku(w http.ResponseWriter, r *http.Request) {
	if s.Danmaku == nil {
		writeError(w, http.StatusServiceUnavailable, "弹幕数据源未配置", "请检查服务启动日志")
		return
	}
	seriesID, vid, ok := streamPathIDs(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	from := int64(0)
	if raw := query.Get("from"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "请求参数不对", "from 需是非负毫秒数（30s 对齐后的窗口起点）")
			return
		}
		from = n
	}
	duration := int64(0)
	if raw := query.Get("duration"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "请求参数不对", "duration 需是正毫秒数（全集时长，取流接口的 duration_ms）")
			return
		}
		duration = n
	}
	if duration <= 0 {
		writeError(w, http.StatusBadRequest, "请求参数不对", "duration 需是正毫秒数（全集时长，取流接口的 duration_ms）")
		return
	}
	if from >= duration {
		// 片尾空窗：直接收口，不打上游
		writeJSON(w, http.StatusOK, danmakuPayload{Items: []danmakuItem{}, NextMS: duration})
		return
	}
	items, nextMS, err := s.Danmaku.DanmakuWindow(r.Context(), seriesID, vid, from, duration)
	if err != nil {
		writeError(w, http.StatusBadGateway, "弹幕加载失败，请稍后重试", err.Error())
		return
	}
	payload := danmakuPayload{Items: make([]danmakuItem, 0, len(items)), NextMS: nextMS}
	for _, item := range items {
		payload.Items = append(payload.Items, danmakuItem{ID: item.ID, OffsetMS: item.TimeMS, Text: item.Text})
	}
	writeJSON(w, http.StatusOK, payload)
}
