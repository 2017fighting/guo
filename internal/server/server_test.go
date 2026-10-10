package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/2017fighting/guo/internal/hongguo"
)

// fakeCatalog 目录源桩：记录收到的查询，返回预设页/枚举/错误。
type fakeCatalog struct {
	query   hongguo.CatalogQuery
	page    *hongguo.CatalogPage
	filters *hongguo.CatalogFilters
	err     error
}

func (f *fakeCatalog) CatalogPage(ctx context.Context, q hongguo.CatalogQuery) (*hongguo.CatalogPage, error) {
	f.query = q
	return f.page, f.err
}

func (f *fakeCatalog) CatalogFilters(ctx context.Context) (*hongguo.CatalogFilters, error) {
	return f.filters, f.err
}

func newTestServer(fake *fakeCatalog, static fstest.MapFS) *httptest.Server {
	srv := &Server{Catalog: fake}
	if static != nil {
		srv.Static = static
	}
	ts := httptest.NewServer(srv.Handler())
	return ts
}

func decodeJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return body
}

// ---- GET /api/v1/catalog ----

func TestCatalogRoute(t *testing.T) {
	fake := &fakeCatalog{page: &hongguo.CatalogPage{
		Items:  []hongguo.CatalogItem{{SeriesID: "700001", Title: "宴律", Status: "完结", Heat: "1.2亿"}},
		Offset: 18, SessionID: "sess-b", HasMore: true,
	}}
	ts := newTestServer(fake, nil)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/catalog?genre=short_play&offset=18&session_id=sess-a")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decodeJSON(t, resp)
	if body["genre"] != "short_play" {
		t.Errorf("genre echo = %v", body["genre"])
	}
	if body["offset"] != float64(18) || body["session_id"] != "sess-b" || body["has_more"] != true {
		t.Errorf("cursor = %v/%v/%v", body["offset"], body["session_id"], body["has_more"])
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %v", body["items"])
	}
	item, _ := items[0].(map[string]any)
	if item["series_id"] != "700001" || item["status"] != "完结" || item["heat"] != "1.2亿" {
		t.Errorf("item = %v", item)
	}
	// 查询参数透传给源
	if fake.query.Genre != "short_play" || fake.query.Offset != 18 || fake.query.SessionID != "sess-a" {
		t.Errorf("source query = %+v", fake.query)
	}
}

func TestCatalogRouteDefaultsAndValidation(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		status  int
		message string
	}{
		{"缺省 genre=all offset=0", "/api/v1/catalog", http.StatusOK, ""},
		{"非法 genre", "/api/v1/catalog?genre=kids", http.StatusBadRequest, "请求参数不对"},
		{"负 offset", "/api/v1/catalog?offset=-1", http.StatusBadRequest, "请求参数不对"},
		{"offset 非数字", "/api/v1/catalog?offset=abc", http.StatusBadRequest, "请求参数不对"},
		{"上游失败", "/api/v1/catalog", http.StatusBadGateway, "目录数据加载失败，请稍后重试"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeCatalog{page: &hongguo.CatalogPage{}, err: nil}
			if tc.status == http.StatusBadGateway {
				fake.err = errors.New("App 分页未前进，请稍后重试")
			}
			ts := newTestServer(fake, nil)
			defer ts.Close()
			resp, err := http.Get(ts.URL + tc.url)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.status)
			}
			if tc.status == http.StatusOK {
				if fake.query.Genre != hongguo.CatalogGenreAll || fake.query.Offset != 0 {
					t.Errorf("default query = %+v", fake.query)
				}
				return
			}
			body := decodeJSON(t, resp)
			if body["message"] != tc.message {
				t.Errorf("message = %v, want %q", body["message"], tc.message)
			}
			if hint, _ := body["hint"].(string); hint == "" {
				t.Errorf("hint missing: %v", body)
			}
		})
	}
}

// ---- GET /api/v1/catalog/filters ----

func TestCatalogFiltersRoute(t *testing.T) {
	fake := &fakeCatalog{filters: &hongguo.CatalogFilters{
		Themes:      []hongguo.CatalogFilterOption{{ID: "都市", Name: "都市"}},
		Statuses:    []hongguo.CatalogFilterOption{{ID: "", Name: "全部"}, {ID: "ongoing", Name: "连载中"}, {ID: "finished", Name: "已完结"}},
		OnlineTimes: []hongguo.CatalogFilterOption{{ID: "", Name: "不限"}},
		Source:      "fallback",
	}}
	ts := newTestServer(fake, nil)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/v1/catalog/filters")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decodeJSON(t, resp)
	if body["source"] != "fallback" {
		t.Errorf("source = %v", body["source"])
	}
	themes, _ := body["themes"].([]any)
	if len(themes) != 1 {
		t.Errorf("themes = %v", themes)
	}
}

// ---- 静态资源（web/dist 注入 fs.FS；embed 接线在后续工单） ----

func TestStaticServing(t *testing.T) {
	static := fstest.MapFS{
		"index.html":    {Data: []byte("<html>guo</html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	ts := newTestServer(&fakeCatalog{page: &hongguo.CatalogPage{}}, static)
	defer ts.Close()

	cases := []struct {
		path     string
		status   int
		bodyPart string
	}{
		{"/", http.StatusOK, "guo"},
		{"/assets/app.js", http.StatusOK, "console.log"},
		{"/detail/700001", http.StatusOK, "guo"}, // SPA 回落 index.html
		{"/api/v1/unknown", http.StatusNotFound, "message"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			resp, err := http.Get(ts.URL + tc.path)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.status {
				t.Fatalf("%s status = %d, want %d", tc.path, resp.StatusCode, tc.status)
			}
			var buf [512]byte
			n, _ := resp.Body.Read(buf[:])
			if !strings.Contains(string(buf[:n]), tc.bodyPart) {
				t.Errorf("%s body = %q, want part %q", tc.path, string(buf[:n]), tc.bodyPart)
			}
		})
	}
}

func TestNoStaticRoot(t *testing.T) {
	ts := newTestServer(&fakeCatalog{page: &hongguo.CatalogPage{}}, nil)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decodeJSON(t, resp)
	if msg, _ := body["message"].(string); msg == "" {
		t.Errorf("message missing: %v", body)
	}
}
