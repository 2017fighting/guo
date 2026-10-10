package server

// /api/v1/drama/{id}/episodes/{vid}/stream（取流）与 /api/v1/stream/*（媒体代理）
// 路由用例：形状、参数校验、Range 转发、按线路 Referer、403/410 重取换址。

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/2017fighting/guo/internal/hongguo"
	"github.com/2017fighting/guo/internal/pipeline"
)

// fakePlaySource 播放取流假源：按序消费预设结果，记录线路/画质参数。
type fakePlaySource struct {
	mu      sync.Mutex
	calls   []string // "seriesID:vid:line:quality"
	results []*hongguo.PlayStream
	err     error
}

func (f *fakePlaySource) ResolvePlayStream(ctx context.Context, seriesID, vid, line string, quality int) (*hongguo.PlayStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("%s:%s:%s:%d", seriesID, vid, line, quality))
	if f.err != nil {
		return nil, f.err
	}
	if len(f.results) == 0 {
		return nil, fmt.Errorf("剧集 %s/%s 无流", seriesID, vid)
	}
	play := f.results[0]
	f.results = f.results[1:]
	return play, nil
}

func (f *fakePlaySource) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// newUpstream 可控媒体上游：支持 Range/206，可按路径前缀连续 410 若干次
// （模拟 CDN 直链过期），记录收到的 Range 与 Referer。
func newUpstream(t *testing.T, body []byte, failPrefixes map[string]int) *upstreamFixture {
	t.Helper()
	u := &upstreamFixture{body: body, failURLs: failPrefixes}
	ts := httptest.NewServer(http.HandlerFunc(u.serve))
	t.Cleanup(ts.Close)
	u.baseURL = ts.URL
	return u
}

type upstreamFixture struct {
	baseURL  string
	mu       sync.Mutex
	body     []byte
	ranges   []string
	referers []string
	failURLs map[string]int // 路径前缀 → 仍需 410 的次数
}

func (u *upstreamFixture) serve(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	u.ranges = append(u.ranges, r.Header.Get("Range"))
	u.referers = append(u.referers, r.Header.Get("Referer"))
	remaining := 0
	for prefix, n := range u.failURLs {
		if strings.HasPrefix(r.URL.Path, prefix) && n > 0 {
			remaining = n
			u.failURLs[prefix] = n - 1
			break
		}
	}
	body := u.body
	u.mu.Unlock()
	if remaining > 0 {
		w.WriteHeader(http.StatusGone)
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Accept-Ranges", "bytes")
	if rng := r.Header.Get("Range"); rng != "" {
		var start int64
		fmt.Sscanf(rng, "bytes=%d-", &start)
		if start >= int64(len(body)) {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(body)-1, len(body)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(body[start:])
		return
	}
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

// ---- GET /api/v1/drama/{seriesID}/episodes/{vid}/stream ----

func TestEpisodeStreamRouteShape(t *testing.T) {
	up := newUpstream(t, []byte("MP4DATA-0123456789"), map[string]int{})
	fake := &fakePlaySource{results: []*hongguo.PlayStream{{
		Stream: &pipeline.Stream{URL: up.baseURL + "/a.mp4", Referer: "https://hongguoduanju.com/", DurationMS: 100000},
		Line:   hongguo.LineWeb, Vertical: true,
	}}}
	ts := httptestUnstarted(&Server{Streams: fake})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/drama/700001/episodes/8000011/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decodeJSON(t, resp)
	if body["line"] != "web" || body["line_label"] != "官网" {
		t.Errorf("line = %v/%v", body["line"], body["line_label"])
	}
	proxyURL, _ := body["proxy_url"].(string)
	if proxyURL == "" {
		t.Fatalf("proxy_url missing: %v", body)
	}
	if !strings.HasPrefix(proxyURL, "/api/v1/stream/700001/8000011?") {
		t.Errorf("proxy_url = %q", proxyURL)
	}
	// 画质档：恒有「自动」在前，线路档位随后
	qualities, _ := body["qualities"].([]any)
	if len(qualities) != 1 {
		t.Fatalf("qualities = %v", qualities)
	}
	q0, _ := qualities[0].(map[string]any)
	if q0["id"] != float64(0) || q0["label"] != "自动" {
		t.Errorf("q0 = %v", q0)
	}
	if body["duration_ms"] != float64(100000) || body["vertical"] != true || body["cenc"] != false {
		t.Errorf("payload = %v", body)
	}
}

func TestEpisodeStreamRouteLineAndQualityPassthrough(t *testing.T) {
	up := newUpstream(t, []byte("x"), map[string]int{})
	fake := &fakePlaySource{results: []*hongguo.PlayStream{{
		Stream: &pipeline.Stream{URL: up.baseURL + "/b.mp4", CENCKeyHex: "aabb", Quality: 720},
		Line:   hongguo.LineApp, Qualities: []hongguo.QualityOption{{ID: 720, Label: "720p"}}, Vertical: false,
	}}}
	ts := httptestUnstarted(&Server{Streams: fake})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/drama/700001/episodes/8000011/stream?line=app&quality=720")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := decodeJSON(t, resp)
	if fake.callCount() != 1 || fake.calls[0] != "700001:8000011:app:720" {
		t.Fatalf("source calls = %v", fake.calls)
	}
	if body["quality"] != float64(720) || body["cenc"] != true {
		t.Errorf("payload = %v", body)
	}
	proxyURL, _ := body["proxy_url"].(string)
	if !strings.Contains(proxyURL, "line=app") || !strings.Contains(proxyURL, "q=720") {
		t.Errorf("proxy_url = %q（应携带 line/q 便于换址重取）", proxyURL)
	}
	qualities, _ := body["qualities"].([]any)
	if len(qualities) != 2 {
		t.Errorf("qualities = %v（自动 + 720p）", body["qualities"])
	}
}

func TestEpisodeStreamRouteValidation(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		status  int
		message string
	}{
		{"非法剧 ID", "/api/v1/drama/abc/episodes/8000011/stream", http.StatusBadRequest, "剧 ID 不对"},
		{"非法分集 ID", "/api/v1/drama/700001/episodes/xyz/stream", http.StatusBadRequest, "分集 ID 不对"},
		{"未知线路", "/api/v1/drama/700001/episodes/8000011/stream?line=cdn", http.StatusBadRequest, "线路不对"},
		{"负画质", "/api/v1/drama/700001/episodes/8000011/stream?quality=-1", http.StatusBadRequest, "画质档不对"},
		{"取流失败", "/api/v1/drama/700001/episodes/8000011/stream", http.StatusBadGateway, "取流失败，请稍后重试或切换线路"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakePlaySource{}
			if tc.name == "取流失败" {
				fake.err = fmt.Errorf("三条线路都取不到")
			}
			ts := httptestUnstarted(&Server{Streams: fake})
			defer ts.Close()
			resp, err := http.Get(ts.URL + tc.url)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.status)
			}
			body := decodeJSON(t, resp)
			if body["message"] != tc.message {
				t.Errorf("message = %v", body["message"])
			}
		})
	}
}

func TestEpisodeStreamRouteNotConfigured(t *testing.T) {
	ts := httptestUnstarted(&Server{})
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/v1/drama/700001/episodes/8000011/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if body := decodeJSON(t, resp); body["message"] != "取流数据源未配置" {
		t.Errorf("message = %v", body["message"])
	}
}

// ---- GET /api/v1/stream/{seriesID}/{vid}：媒体代理 ----

// fetchProxyURL 走取流接口拿代理地址（真实链路），代理测试基于它继续。
func fetchProxyURL(t *testing.T, tsURL string, query string) string {
	t.Helper()
	resp, err := http.Get(tsURL + "/api/v1/drama/700001/episodes/8000011/stream" + query)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d", resp.StatusCode)
	}
	body := decodeJSON(t, resp)
	proxyURL, _ := body["proxy_url"].(string)
	if proxyURL == "" {
		t.Fatalf("proxy_url missing: %v", body)
	}
	return proxyURL
}

func TestStreamProxyRangePassthrough(t *testing.T) {
	up := newUpstream(t, []byte("0123456789ABCDEF"), map[string]int{})
	fake := &fakePlaySource{results: []*hongguo.PlayStream{{
		Stream: &pipeline.Stream{URL: up.baseURL + "/a.mp4", Referer: "https://hongguoduanju.com/", DurationMS: 1000},
		Line:   hongguo.LineWeb, Vertical: true,
	}}}
	ts := httptestUnstarted(&Server{Streams: fake})
	defer ts.Close()

	proxyURL := fetchProxyURL(t, ts.URL, "")
	req, _ := http.NewRequest(http.MethodGet, ts.URL+proxyURL, nil)
	req.Header.Set("Range", "bytes=4-")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if cr := resp.Header.Get("Content-Range"); !strings.Contains(cr, "bytes 4-15/16") {
		t.Errorf("Content-Range = %q", cr)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "video/mp4" {
		t.Errorf("Content-Type = %q", ct)
	}
	data := make([]byte, 32)
	n, _ := resp.Body.Read(data)
	if string(data[:n]) != "456789ABCDEF" {
		t.Errorf("body = %q", data[:n])
	}
	if len(up.ranges) == 0 || up.ranges[0] != "bytes=4-" {
		t.Errorf("upstream Range = %v", up.ranges)
	}
}

func TestStreamProxyRefererByLine(t *testing.T) {
	cases := []struct {
		line    string
		referer string
	}{
		{hongguo.LineWeb, "https://hongguoduanju.com/"},
		{hongguo.LineApp, ""},
	}
	for _, tc := range cases {
		t.Run("line="+tc.line, func(t *testing.T) {
			up := newUpstream(t, []byte("0123456789"), map[string]int{})
			fake := &fakePlaySource{results: []*hongguo.PlayStream{{
				Stream: &pipeline.Stream{URL: up.baseURL + "/a.mp4"}, Line: tc.line, Vertical: true,
			}}}
			ts := httptestUnstarted(&Server{Streams: fake})
			defer ts.Close()
			proxyURL := fetchProxyURL(t, ts.URL, "?line="+tc.line)
			resp, err := http.Get(ts.URL + proxyURL)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			if len(up.referers) == 0 || up.referers[0] != tc.referer {
				t.Errorf("upstream Referer = %v, want %q", up.referers, tc.referer)
			}
		})
	}
}

func TestStreamProxyReResolveOnExpiry(t *testing.T) {
	up := newUpstream(t, []byte("FRESHDATA"), map[string]int{"/stale.mp4": 1})
	stale := &hongguo.PlayStream{
		Stream: &pipeline.Stream{URL: up.baseURL + "/stale.mp4", DurationMS: 1000},
		Line:   hongguo.LineApp, Vertical: true,
	}
	fresh := &hongguo.PlayStream{
		Stream: &pipeline.Stream{URL: up.baseURL + "/fresh.mp4", DurationMS: 1000},
		Line:   hongguo.LineApp, Vertical: true,
	}
	fake := &fakePlaySource{results: []*hongguo.PlayStream{stale, fresh}}
	ts := httptestUnstarted(&Server{Streams: fake})
	defer ts.Close()

	proxyURL := fetchProxyURL(t, ts.URL, "?line=app")
	resp, err := http.Get(ts.URL + proxyURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	data := make([]byte, 32)
	n, _ := resp.Body.Read(data)
	if string(data[:n]) != "FRESHDATA" {
		t.Errorf("body = %q（应重取后继续服务）", data[:n])
	}
	// 取流 1 次（端点）+ 换址重取 1 次，重取沿用同线路
	if fake.callCount() != 2 || fake.calls[1] != "700001:8000011:app:0" {
		t.Errorf("calls = %v", fake.calls)
	}
}

func TestStreamProxyExpiryExhausted(t *testing.T) {
	up := newUpstream(t, []byte("x"), map[string]int{"/a.mp4": 99, "/b.mp4": 99})
	first := &hongguo.PlayStream{Stream: &pipeline.Stream{URL: up.baseURL + "/a.mp4"}, Line: hongguo.LineWeb, Vertical: true}
	second := &hongguo.PlayStream{Stream: &pipeline.Stream{URL: up.baseURL + "/b.mp4"}, Line: hongguo.LineWeb, Vertical: true}
	fake := &fakePlaySource{results: []*hongguo.PlayStream{first, second}}
	ts := httptestUnstarted(&Server{Streams: fake})
	defer ts.Close()

	proxyURL := fetchProxyURL(t, ts.URL, "")
	resp, err := http.Get(ts.URL + proxyURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	// 只重取一次，不无限循环
	if fake.callCount() != 2 {
		t.Errorf("calls = %d（应恰好重取一次）", fake.callCount())
	}
}
