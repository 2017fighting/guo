package server

// GET /api/v1/rankings 处理器测试：响应形态、参数校验、错误口径。

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/2017fighting/guo/internal/hongguo/rankings"
)

// fakeRankings 榜单缓存桩。
type fakeRankings struct {
	list   string
	offset int
	cursor string
	called bool
	page   *rankings.Page
	err    error
}

func (f *fakeRankings) Page(ctx context.Context, list string, offset int, cursor string) (*rankings.Page, error) {
	f.called = true
	f.list, f.offset, f.cursor = list, offset, cursor
	return f.page, f.err
}

func newRankingsTestServer(fake *fakeRankings) *httptest.Server {
	srv := &Server{Rankings: fake}
	ts := httptest.NewServer(srv.Handler())
	return ts
}

func getRankings(t *testing.T, ts *httptest.Server, path string) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	resp.Body.Close()
	return resp, body
}

func TestRankingsRoute(t *testing.T) {
	fake := &fakeRankings{page: &rankings.Page{
		List:      "ranklist_hot_sc",
		Items:     []rankings.Item{{SeriesID: "7101", Title: "龙医出山", Rank: 1, Heat: "4868万热度"}},
		Offset:    10,
		SessionID: "r.eyJzIjoic2Vzcy0xIn0",
		HasMore:   true,
		UpdatedAt: 1791618347,
	}}
	ts := newRankingsTestServer(fake)
	defer ts.Close()

	resp, body := getRankings(t, ts, "/api/v1/rankings?list=ranklist_hot_sc")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 %d", resp.StatusCode)
	}
	if fake.list != "ranklist_hot_sc" || fake.offset != 0 || fake.cursor != "" {
		t.Fatalf("参数透传不符: %s/%d/%q", fake.list, fake.offset, fake.cursor)
	}
	if body["list"] != "ranklist_hot_sc" || body["has_more"] != true {
		t.Fatalf("响应形态不符: %v", body)
	}
	if body["offset"].(float64) != 10 || body["updated_at"].(float64) != 1791618347 {
		t.Fatalf("游标/更新时间不符: %v", body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items 应有 1 条: %v", body["items"])
	}
	item, _ := items[0].(map[string]any)
	if item["series_id"] != "7101" || item["rank"].(float64) != 1 || item["heat"] != "4868万热度" {
		t.Fatalf("条目形态不符: %v", item)
	}
}

func TestRankingsCursorAndOffsetParams(t *testing.T) {
	fake := &fakeRankings{page: &rankings.Page{List: "ranklist_prestige"}}
	ts := newRankingsTestServer(fake)
	defer ts.Close()

	getRankings(t, ts, "/api/v1/rankings?list=ranklist_prestige&offset=10&session_id=r.eyJ6IjoiMSJ9")
	if fake.offset != 10 || fake.cursor != "r.eyJ6IjoiMSJ9" {
		t.Fatalf("offset/session_id 应透传: %d/%q", fake.offset, fake.cursor)
	}
}

func TestRankingsInvalidList(t *testing.T) {
	fake := &fakeRankings{}
	ts := newRankingsTestServer(fake)
	defer ts.Close()

	resp, body := getRankings(t, ts, "/api/v1/rankings?list=bogus")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("未知榜单应 400，实际 %d", resp.StatusCode)
	}
	if _, ok := body["message"]; !ok {
		t.Fatalf("错误体应含 message: %v", body)
	}
	if fake.called {
		t.Fatal("未知榜单不应打到数据源")
	}
}

func TestRankingsInvalidOffset(t *testing.T) {
	fake := &fakeRankings{page: &rankings.Page{List: "ranklist_hot_sc"}}
	ts := newRankingsTestServer(fake)
	defer ts.Close()

	resp, _ := getRankings(t, ts, "/api/v1/rankings?list=ranklist_hot_sc&offset=-5")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("负 offset 应 400，实际 %d", resp.StatusCode)
	}
	resp, _ = getRankings(t, ts, "/api/v1/rankings?list=ranklist_hot_sc&offset=abc")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("非数字 offset 应 400，实际 %d", resp.StatusCode)
	}
}

func TestRankingsSourceError(t *testing.T) {
	fake := &fakeRankings{err: errors.New("api3 连接超时")}
	ts := newRankingsTestServer(fake)
	defer ts.Close()

	resp, body := getRankings(t, ts, "/api/v1/rankings?list=ranklist_hot_sc")
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("源站失败应 502，实际 %d", resp.StatusCode)
	}
	if body["message"] == "" || body["hint"] == "" {
		t.Fatalf("错误体应含 message+hint: %v", body)
	}
}

func TestRankingsSourceMissing(t *testing.T) {
	ts := httptest.NewServer((&Server{}).Handler())
	defer ts.Close()

	resp, body := getRankings(t, ts, "/api/v1/rankings?list=ranklist_hot_sc")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("未配置数据源应 503，实际 %d", resp.StatusCode)
	}
	if body["message"] == "" {
		t.Fatalf("错误体应含 message: %v", body)
	}
}
