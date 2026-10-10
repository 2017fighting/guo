package server

// /api/v1/drama/{seriesID}/episodes/{vid}/stream：播放取流（线路偏好 + 画质档），
// 返回媒体代理地址 + 画质档 + 时长 + 画面方向。
// /api/v1/stream/{seriesID}/{vid}：媒体代理——Range 转发、按线路 Referer 策略、
// 上游 403/410（直链约 30 分钟过期）时按同线路/画质重取换址一次继续服务。
// 换址语义对齐 pipeline 引擎 download 的 errURLExpired 循环（那边落盘、这边直通）。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/2017fighting/guo/internal/hongguo"
	"github.com/2017fighting/guo/internal/pipeline"
)

// StreamSource 播放取流数据源（*hongguo.Client 实现；接口让 handler 可测）。
type StreamSource interface {
	ResolvePlayStream(ctx context.Context, seriesID, vid, line string, quality int) (*hongguo.PlayStream, error)
}

// 线路展示名（播放页线路下拉）。
var lineLabels = map[string]string{
	hongguo.LineWeb:      "官网",
	hongguo.LineApp:      "App",
	hongguo.LineFallback: "兜底",
}

// webReferer 官网线路 CDN 需带 Referer；App/兜底线路 CDN 反而不带（v9 实测：
// 带了 403）——与 hongguo webBaseURL 同源的字面量。
const webReferer = "https://hongguoduanju.com/"

type streamQuality struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
}

type streamPayload struct {
	ProxyURL   string          `json:"proxy_url"`
	Line       string          `json:"line"`
	LineLabel  string          `json:"line_label"`
	Qualities  []streamQuality `json:"qualities"`
	Quality    int             `json:"quality"` // 生效画质（0 = 自动）
	DurationMS int64           `json:"duration_ms"`
	Vertical   bool            `json:"vertical"`
	CENC       bool            `json:"cenc"` // CENC 加密流浏览器可能播不了（提示切线路）
}

// handleEpisodeStream GET /api/v1/drama/{seriesID}/episodes/{vid}/stream?line=&quality=
func (s *Server) handleEpisodeStream(w http.ResponseWriter, r *http.Request) {
	if s.Streams == nil {
		writeError(w, http.StatusServiceUnavailable, "取流数据源未配置", "请检查服务启动日志")
		return
	}
	seriesID, vid, ok := streamPathIDs(w, r)
	if !ok {
		return
	}
	line, quality, ok := streamQuery(w, r)
	if !ok {
		return
	}
	play, err := s.Streams.ResolvePlayStream(r.Context(), seriesID, vid, line, quality)
	if err != nil {
		writeError(w, http.StatusBadGateway, "取流失败，请稍后重试或切换线路", err.Error())
		return
	}
	qualities := []streamQuality{{ID: 0, Label: "自动"}}
	for _, opt := range play.Qualities {
		if opt.ID > 0 {
			qualities = append(qualities, streamQuality(opt))
		}
	}
	writeJSON(w, http.StatusOK, streamPayload{
		ProxyURL:   streamProxyURL(seriesID, vid, play.Stream.URL, play.Line, quality),
		Line:       play.Line,
		LineLabel:  lineLabels[play.Line],
		Qualities:  qualities,
		Quality:    play.Stream.Quality,
		DurationMS: play.Stream.DurationMS,
		Vertical:   play.Vertical,
		CENC:       play.Stream.CENCKeyHex != "",
	})
}

// handleStreamProxy GET /api/v1/stream/{seriesID}/{vid}?u=&line=&q=
// u 缺省时先取流；403/410 时按 line/q 重取换址一次（Range 重放）。
func (s *Server) handleStreamProxy(w http.ResponseWriter, r *http.Request) {
	if s.Streams == nil {
		writeError(w, http.StatusServiceUnavailable, "取流数据源未配置", "请检查服务启动日志")
		return
	}
	seriesID, vid, ok := streamPathIDs(w, r)
	if !ok {
		return
	}
	line, quality, ok := streamQuery(w, r)
	if !ok {
		return
	}
	upstream := r.URL.Query().Get("u")
	if upstream == "" {
		play, err := s.Streams.ResolvePlayStream(r.Context(), seriesID, vid, line, quality)
		if err != nil {
			writeError(w, http.StatusBadGateway, "取流失败，请稍后重试或切换线路", err.Error())
			return
		}
		upstream = play.Stream.URL
	}
	resp, err := proxyUpstream(r, upstream, line)
	if errors.Is(err, errUpstreamExpired) {
		play, rerr := s.Streams.ResolvePlayStream(r.Context(), seriesID, vid, line, quality)
		if rerr != nil {
			writeError(w, http.StatusBadGateway, "流地址已过期，重取也失败了", rerr.Error())
			return
		}
		resp, err = proxyUpstream(r, play.Stream.URL, line)
	}
	if err != nil {
		if errors.Is(err, errUpstreamExpired) {
			writeError(w, http.StatusBadGateway, "媒体源持续拒绝访问（403/410）",
				"已重取换址仍失败，多半是直链再过期，请刷新或切换线路")
			return
		}
		writeError(w, http.StatusBadGateway, "媒体代理上游失败", err.Error())
		return
	}
	defer resp.Body.Close()
	for _, header := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if value := resp.Header.Get(header); value != "" {
			w.Header().Set(header, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

var errUpstreamExpired = errors.New("媒体上游 403/410（直链过期）")

// proxyUpstream 按线路 Referer 策略发起上游请求（Range 透传）。
func proxyUpstream(r *http.Request, upstream, line string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upstream, nil)
	if err != nil {
		return nil, err
	}
	if rng := r.Header.Get("Range"); rng != "" {
		req.Header.Set("Range", rng)
	}
	if line == hongguo.LineWeb {
		req.Header.Set("Referer", webReferer)
	}
	req.Header.Set("User-Agent", pipeline.IPhoneUA)
	req.Header.Set("Accept", "*/*")
	resp, err := streamHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusPartialContent:
		return resp, nil
	case http.StatusForbidden, http.StatusGone:
		resp.Body.Close()
		return nil, errUpstreamExpired
	default:
		status := resp.StatusCode
		resp.Body.Close()
		return nil, fmt.Errorf("媒体源 HTTP %d", status)
	}
}

// streamHTTPClient 代理上游客户端：只限制响应头等待，不限整体时长（长视频）。
var streamHTTPClient = &http.Client{
	Transport: &http.Transport{ResponseHeaderTimeout: 20 * time.Second},
}

func streamProxyURL(seriesID, vid, upstream, line string, quality int) string {
	query := url.Values{"u": {upstream}}
	if line != "" {
		query.Set("line", line)
	}
	if quality > 0 {
		query.Set("q", strconv.Itoa(quality))
	}
	return "/api/v1/stream/" + seriesID + "/" + vid + "?" + query.Encode()
}

func streamPathIDs(w http.ResponseWriter, r *http.Request) (seriesID, vid string, ok bool) {
	seriesID, vid = r.PathValue("seriesID"), r.PathValue("vid")
	if !seriesIDPattern.MatchString(seriesID) {
		writeError(w, http.StatusBadRequest, "剧 ID 不对", "series_id 应为 1–32 位数字（红剧集 ID）")
		return "", "", false
	}
	if !seriesIDPattern.MatchString(vid) {
		writeError(w, http.StatusBadRequest, "分集 ID 不对", "vid 应为 1–32 位数字（红果分集视频 ID）")
		return "", "", false
	}
	return seriesID, vid, true
}

func streamQuery(w http.ResponseWriter, r *http.Request) (line string, quality int, ok bool) {
	query := r.URL.Query()
	line = query.Get("line")
	if line != "" && lineLabels[line] == "" {
		writeError(w, http.StatusBadRequest, "线路不对", "line 可为空（自动）或 web/app/fallback")
		return "", 0, false
	}
	// 取流端点用全名 quality，代理地址用短名 q（两者取一）
	raw := query.Get("quality")
	if raw == "" {
		raw = query.Get("q")
	}
	if raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "画质档不对", "quality 需是非负整数（0 = 自动）")
			return "", 0, false
		}
		quality = n
	}
	return line, quality, true
}
