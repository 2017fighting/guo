package server

// /api/v1/drama/{id}/episodes/{vid}/danmaku 路由用例：窗口拉取、
// 游标推进、参数校验、末端空窗。

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/2017fighting/guo/internal/ass"
)

// fakeDanmakuSource 弹幕窗口假源：记录参数，返回预设窗口。
type fakeDanmakuSource struct {
	mu       sync.Mutex
	calls    []string // "seriesID:vid:start:duration"
	items    []ass.Comment
	nextMS   int64
	err      error
	seriesID string
	vid      string
}

func (f *fakeDanmakuSource) DanmakuWindow(ctx context.Context, seriesID, vid string, startMS, durationMS int64) ([]ass.Comment, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("%s:%s:%d:%d", seriesID, vid, startMS, durationMS))
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.items, f.nextMS, nil
}

func newDanmakuFixture(items []ass.Comment, nextMS int64) (*fakeDanmakuSource, *Server) {
	fake := &fakeDanmakuSource{items: items, nextMS: nextMS, seriesID: "700001", vid: "8000011"}
	return fake, &Server{Danmaku: fake}
}

func TestDanmakuRouteWindow(t *testing.T) {
	items := []ass.Comment{
		{ID: "c1", Text: "前方高能", TimeMS: 30500},
		{ID: "c2", Text: "这集封神", TimeMS: 31200},
	}
	fake, srv := newDanmakuFixture(items, 60000)
	ts := httptestUnstarted(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/drama/700001/episodes/8000011/danmaku?from=30000&duration=624000")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decodeJSON(t, resp)
	// 窗口起点/时长透传给源（from 由前端对齐 30s 边界）
	if len(fake.calls) != 1 || fake.calls[0] != "700001:8000011:30000:624000" {
		t.Fatalf("source calls = %v", fake.calls)
	}
	got, _ := body["items"].([]any)
	if len(got) != 2 {
		t.Fatalf("items = %v", body["items"])
	}
	first, _ := got[0].(map[string]any)
	if first["id"] != "c1" || first["offset_ms"] != float64(30500) || first["text"] != "前方高能" {
		t.Errorf("item0 = %v", first)
	}
	if body["next_ms"] != float64(60000) {
		t.Errorf("next_ms = %v（应回源游标供下一窗拉取）", body["next_ms"])
	}
}

func TestDanmakuRouteTailWindowEmpty(t *testing.T) {
	fake, srv := newDanmakuFixture(nil, 624000)
	ts := httptestUnstarted(srv)
	defer ts.Close()

	// from ≥ duration：片尾空窗，直接收口，不打上游
	resp, err := http.Get(ts.URL + "/api/v1/drama/700001/episodes/8000011/danmaku?from=624000&duration=624000")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decodeJSON(t, resp)
	if items, _ := body["items"].([]any); len(items) != 0 {
		t.Errorf("items = %v", items)
	}
	if body["next_ms"] != float64(624000) {
		t.Errorf("next_ms = %v", body["next_ms"])
	}
	if len(fake.calls) != 0 {
		t.Errorf("tail window should not hit source: %v", fake.calls)
	}
}

func TestDanmakuRouteValidationAndErrors(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		status  int
		message string
	}{
		{"缺时长", "/api/v1/drama/700001/episodes/8000011/danmaku?from=0", http.StatusBadRequest, "请求参数不对"},
		{"负时长", "/api/v1/drama/700001/episodes/8000011/danmaku?from=0&duration=-1", http.StatusBadRequest, "请求参数不对"},
		{"负起点", "/api/v1/drama/700001/episodes/8000011/danmaku?from=-5&duration=624000", http.StatusBadRequest, "请求参数不对"},
		{"非法剧 ID", "/api/v1/drama/abc/episodes/8000011/danmaku?duration=624000", http.StatusBadRequest, "剧 ID 不对"},
		{"非法分集 ID", "/api/v1/drama/700001/episodes/xyz/danmaku?duration=624000", http.StatusBadRequest, "分集 ID 不对"},
		{"上游失败", "/api/v1/drama/700001/episodes/8000011/danmaku?from=0&duration=624000", http.StatusBadGateway, "弹幕加载失败，请稍后重试"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake, srv := newDanmakuFixture(nil, 0)
			if tc.name == "上游失败" {
				fake.err = fmt.Errorf("签名被拒")
			}
			ts := httptestUnstarted(srv)
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

func TestDanmakuRouteNotConfigured(t *testing.T) {
	ts := httptestUnstarted(&Server{})
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/v1/drama/700001/episodes/8000011/danmaku?from=0&duration=624000")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if body := decodeJSON(t, resp); body["message"] != "弹幕数据源未配置" {
		t.Errorf("message = %v", body["message"])
	}
}
