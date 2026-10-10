package server

// /api/v1/drama 详情路由用例：形状、校验、上游失败。

import (
	"net/http"
	"testing"

	"github.com/2017fighting/guo/internal/pipeline"
)

func TestDramaRoute(t *testing.T) {
	f := newQueueFixture(t, queueFixtureOpts{})
	defer f.Close()

	resp, err := http.Get(f.ts.URL + "/api/v1/drama/700001")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decodeJSON(t, resp)
	if body["series_id"] != "700001" || body["title"] != "测试剧A" || body["year"] != "2025" {
		t.Errorf("meta = %v", body)
	}
	if body["cover"] != "https://img.example/700001.jpg" {
		t.Errorf("cover = %v", body["cover"])
	}
	genres, _ := body["genres"].([]any)
	if len(genres) != 2 || genres[0] != "都市" {
		t.Errorf("genres = %v", genres)
	}
	eps, _ := body["episodes"].([]any)
	if len(eps) != 3 {
		t.Fatalf("episodes = %v", eps)
	}
	ep1, _ := eps[0].(map[string]any)
	if ep1["index"] != float64(1) || ep1["vid"] != "v7000011" {
		t.Errorf("ep1 = %v", ep1)
	}
}

func TestDramaRouteValidationAndErrors(t *testing.T) {
	f := newQueueFixture(t, queueFixtureOpts{})
	defer f.Close()

	cases := []struct {
		name    string
		url     string
		status  int
		message string
	}{
		{"非法 ID（字母）", "/api/v1/drama/abc", http.StatusBadRequest, "剧 ID 不对"},
		{"ID 过长", "/api/v1/drama/123456789012345678901234567890123", http.StatusBadRequest, "剧 ID 不对"},
		{"源站拉不到", "/api/v1/drama/999999", http.StatusBadGateway, "详情加载失败，请稍后重试"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Get(f.ts.URL + tc.url)
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
			if hint, _ := body["hint"].(string); hint == "" {
				t.Errorf("hint missing: %v", body)
			}
		})
	}
}

// 未配置源（Drama=nil）时给出可读错误而不是 panic。
func TestDramaRouteNotConfigured(t *testing.T) {
	srv := &Server{}
	ts := httptestUnstarted(srv)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/v1/drama/700001")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if body := decodeJSON(t, resp); body["message"] != "详情数据源未配置" {
		t.Errorf("message = %v", body["message"])
	}
}

var _ pipeline.Source = (*fakeDramaSource)(nil)
