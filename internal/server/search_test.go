package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/2017fighting/guo/internal/hongguo"
)

// fakeSearch 搜索/联想源桩：记录收到的关键词，返回预设结果/错误。
type fakeSearch struct {
	query      string
	result     *hongguo.SearchResult
	searchErr  error
	suggests   []hongguo.SuggestItem
	suggestErr error
}

func (f *fakeSearch) Search(ctx context.Context, query string) (*hongguo.SearchResult, error) {
	f.query = query
	return f.result, f.searchErr
}

func (f *fakeSearch) Suggest(ctx context.Context, query string) ([]hongguo.SuggestItem, error) {
	f.query = query
	return f.suggests, f.suggestErr
}

func newSearchTestServer(search SearchSource) *httptest.Server {
	srv := &Server{Search: search}
	return httptest.NewServer(srv.Handler())
}

// ---- GET /api/v1/search ----

func TestSearchRoute(t *testing.T) {
	fake := &fakeSearch{result: &hongguo.SearchResult{
		Query:   "白月光",
		Items:   []hongguo.CatalogItem{{SeriesID: "700001", Title: "白月光", Status: "完结"}},
		Limited: true,
		Warnings: []string{"名称检索暂不可用，已只显示官网搜索结果"},
	}}
	ts := newSearchTestServer(fake)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/search?q=%E7%99%BD%E6%9C%88%E5%85%89")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if fake.query != "白月光" {
		t.Fatalf("透传关键词 = %q", fake.query)
	}
	body := decodeJSON(t, resp)
	if body["query"] != "白月光" || body["limited"] != true {
		t.Fatalf("响应形状不对: %v", body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items 数 = %d", len(items))
	}
	first, _ := items[0].(map[string]any)
	if first["series_id"] != "700001" || first["status"] != "完结" {
		t.Fatalf("条目字段不对: %v", first)
	}
	warnings, _ := body["warnings"].([]any)
	if len(warnings) != 1 {
		t.Fatalf("warnings 数 = %d", len(warnings))
	}
}

func TestSearchRouteEmptyQuery(t *testing.T) {
	fake := &fakeSearch{}
	ts := newSearchTestServer(fake)
	defer ts.Close()

	// 空 q → 空结果而非报错
	resp, err := http.Get(ts.URL + "/api/v1/search")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if fake.query != "" {
		t.Fatal("空 q 不应触达数据源")
	}
	body := decodeJSON(t, resp)
	items, _ := body["items"].([]any)
	warnings, _ := body["warnings"].([]any)
	if len(items) != 0 || len(warnings) != 0 || body["limited"] != false {
		t.Fatalf("空结果形状不对: %v", body)
	}
}

func TestSearchRouteKeywordError(t *testing.T) {
	fake := &fakeSearch{searchErr: hongguo.ErrKeywordInvalid}
	ts := newSearchTestServer(fake)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/search?q=" + strings.Repeat("%E5%89%A7", 81))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body := decodeJSON(t, resp)
	if body["message"] == "" || body["hint"] == "" {
		t.Fatalf("错误体缺 message/hint: %v", body)
	}
}

func TestSearchRouteSourceError(t *testing.T) {
	fake := &fakeSearch{searchErr: errors.New("官网搜索页: HTTP 500；名称索引: HTTP 500")}
	ts := newSearchTestServer(fake)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/search?q=x")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
}

func TestSearchRouteNilSource(t *testing.T) {
	ts := newSearchTestServer(nil)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/search?q=x")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

// ---- GET /api/v1/search/suggest ----

func TestSuggestRoute(t *testing.T) {
	fake := &fakeSearch{suggests: []hongguo.SuggestItem{
		{Name: "白月光她不装了", Type: "short_play_name", SeriesID: "700001"},
		{Name: "白月光题材", Type: "short_play_category"},
		{Name: "小说推荐", Type: ""},
	}}
	ts := newSearchTestServer(fake)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/search/suggest?q=%E7%99%BD")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if fake.query != "白" {
		t.Fatalf("透传关键词 = %q", fake.query)
	}
	body := decodeJSON(t, resp)
	items, _ := body["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("items 数 = %d", len(items))
	}
	first, _ := items[0].(map[string]any)
	if first["name"] != "白月光她不装了" || first["type"] != "short_play_name" || first["series_id"] != "700001" {
		t.Fatalf("首条字段不对: %v", first)
	}
	third, _ := items[2].(map[string]any)
	if _, has := third["series_id"]; has {
		t.Fatalf("无 series_id 不应序列化该字段: %v", third)
	}
}

func TestSuggestRouteEmptyQuery(t *testing.T) {
	fake := &fakeSearch{}
	ts := newSearchTestServer(fake)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/search/suggest")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if fake.query != "" {
		t.Fatal("空 q 不应触达数据源")
	}
	body := decodeJSON(t, resp)
	items, _ := body["items"].([]any)
	if len(items) != 0 {
		t.Fatalf("空结果 items 数 = %d", len(items))
	}
}

func TestSuggestRouteKeywordError(t *testing.T) {
	fake := &fakeSearch{suggestErr: hongguo.ErrKeywordInvalid}
	ts := newSearchTestServer(fake)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/search/suggest?q=" + strings.Repeat("%E5%89%A7", 81))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestSuggestRouteSourceError(t *testing.T) {
	fake := &fakeSearch{suggestErr: errors.New("联想接口 HTTP 502")}
	ts := newSearchTestServer(fake)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/search/suggest?q=x")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
}
