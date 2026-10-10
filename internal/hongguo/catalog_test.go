package hongguo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// ---- 目录 feed（App 分类 landpage 通道，guoapp-reference §1.1） ----

// catalogLandpageFixture 构造 landpage 分页响应（rows 为空时是空数组而非 null）。
func catalogLandpageFixture(next string, hasMore bool, session string, rows ...map[string]any) map[string]any {
	list := make([]any, 0, len(rows))
	for _, row := range rows {
		list = append(list, row)
	}
	data := map[string]any{
		"video_data": list,
		"has_more":   hasMore,
	}
	if next != "" {
		data["next_offset"] = next
	}
	if session != "" {
		data["session_id"] = session
	}
	return map[string]any{"code": 0, "data": data}
}

// catalogCard 构造 landpage 风格剧卡。
func catalogCard(id, title string) map[string]any {
	return map[string]any{
		"series_id_str": id,
		"series_title":  title,
		"series_cover":  "//p.test/" + id + ".jpg",
		"episode_cnt":   "76",
		"series_status": "1",
		"vertical":      true,
		"tags":          []any{"都市", "逆袭"},
	}
}

func TestCatalogShortPlayPage(t *testing.T) {
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reading/distribution/category/landpage/v/" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode body: %v", err)
		}
		json.NewEncoder(w).Encode(catalogLandpageFixture("18", true, "sess-a",
			catalogCard("700001", "宴律，你的白月光回国了"),
			map[string]any{
				"series_id":      "700002",
				"series_name":    "连载剧",
				"cover":          "https://p.test/2.jpg",
				"episode_cnt":    "100",
				"series_status":  "0",
				"hot_score_data": map[string]any{"score": "89010000"},
				"pay_info":       map[string]any{"pay_status": "1"},
				"sub_title_list": []any{
					map[string]any{"content": "都市", "data_type": 3},
					map[string]any{"content": "昨日上新", "data_type": 1},
				},
			},
		))
	}))
	defer srv.Close()

	c := NewClient()
	c.BaseURL = srv.URL
	c.HTTP = srv.Client()
	page, err := c.CatalogPage(context.Background(), CatalogQuery{Genre: CatalogGenreShortPlay})
	if err != nil {
		t.Fatal(err)
	}

	// 请求体形态（guoapp fetchHongguoAppCatalogCategory payload）
	selectItems, _ := payload["select_items"].(map[string]any)
	if payload["req_scene"] != "default" || payload["limit"] != float64(18) {
		t.Errorf("req_scene/limit = %v/%v", payload["req_scene"], payload["limit"])
	}
	if payload["req_type"] != "only_content" || payload["need_selector_panel"] != false {
		t.Errorf("req_type/need_selector_panel = %v/%v", payload["req_type"], payload["need_selector_panel"])
	}
	if payload["client_req_type"] != float64(3) || payload["session_id"] != "" || payload["offset"] != float64(0) {
		t.Errorf("client_req_type/session_id/offset = %v/%v/%v", payload["client_req_type"], payload["session_id"], payload["offset"])
	}
	if genre, _ := selectItems["genre"].([]any); len(genre) != 1 || genre[0] != "short_play" {
		t.Errorf("select_items.genre = %v", selectItems["genre"])
	}
	if sort, _ := selectItems["sort"].([]any); len(sort) != 1 || sort[0] != "online_time" {
		t.Errorf("select_items.sort = %v", selectItems["sort"])
	}

	// 卡片规范化
	if len(page.Items) != 2 {
		t.Fatalf("items = %+v", page.Items)
	}
	first := page.Items[0]
	if first.SeriesID != "700001" || first.Title != "宴律，你的白月光回国了" {
		t.Errorf("first = %+v", first)
	}
	if first.Cover != "https://p.test/700001.jpg" {
		t.Errorf("cover = %q", first.Cover)
	}
	if first.EpisodeCount != "76" || first.Status != "完结" || !first.Vertical {
		t.Errorf("count/status/vertical = %q/%q/%v", first.EpisodeCount, first.Status, first.Vertical)
	}
	if len(first.Tags) != 2 || first.Tags[0] != "都市" {
		t.Errorf("tags = %v", first.Tags)
	}
	second := page.Items[1]
	if second.Status != "连载" || !second.VIP {
		t.Errorf("second status/vip = %q/%v", second.Status, second.VIP)
	}
	if second.Heat != "89010000" {
		t.Errorf("heat = %q", second.Heat)
	}

	// 游标
	if page.Offset != 18 || page.SessionID != "sess-a" || !page.HasMore {
		t.Errorf("cursor = %+v", page)
	}
}

func TestCatalogPageForwardsCursor(t *testing.T) {
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&payload)
		json.NewEncoder(w).Encode(catalogLandpageFixture("36", true, "sess-b", catalogCard("700003", "翻页剧")))
	}))
	defer srv.Close()
	c := NewClient()
	c.BaseURL = srv.URL
	c.HTTP = srv.Client()
	page, err := c.CatalogPage(context.Background(), CatalogQuery{Genre: CatalogGenreComic, Offset: 18, SessionID: "sess-a"})
	if err != nil {
		t.Fatal(err)
	}
	if payload["offset"] != float64(18) || payload["session_id"] != "sess-a" {
		t.Errorf("offset/session_id = %v/%v", payload["offset"], payload["session_id"])
	}
	// offset>0 时 client_req_type 切 2
	if payload["client_req_type"] != float64(2) {
		t.Errorf("client_req_type = %v", payload["client_req_type"])
	}
	if page.Offset != 36 || page.SessionID != "sess-b" {
		t.Errorf("cursor = %+v", page)
	}
}

func TestCatalogPageRejectsInvalidGenre(t *testing.T) {
	c := NewClient()
	if _, err := c.CatalogPage(context.Background(), CatalogQuery{Genre: "unknown"}); err == nil {
		t.Fatal("invalid genre should be rejected")
	}
}

// ---- genre=all：三 genre 轮询合并 + 组合游标 ----

func TestCatalogAllGenresMerge(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		items, _ := payload["select_items"].(map[string]any)
		genre, _ := items["genre"].([]any)
		offset, _ := payload["offset"].(float64)
		// 漫剧第一页即翻完（has_more=false），另两个 genre 继续
		hasMore := !(genre[0] == "comic_series" && offset == 0)
		next := strconv.Itoa(int(offset) + 18)
		session := "s-" + genre[0].(string) + "-" + strconv.Itoa(int(offset))
		seqNum := int(atomic.AddInt32(&calls, 1))
		seq := map[string]string{"short_play": "1", "comic_series": "2", "ai_series": "3"}[genre[0].(string)]
		json.NewEncoder(w).Encode(catalogLandpageFixture(next, hasMore, session,
			catalogCard("9"+seq+strconv.Itoa(int(offset)), "剧"+strconv.Itoa(seqNum)),
		))
	}))
	defer srv.Close()

	c := NewClient()
	c.BaseURL = srv.URL
	c.HTTP = srv.Client()

	page, err := c.CatalogPage(context.Background(), CatalogQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("首轮应合并 3 个 genre 各 18 条槽位（各 1 条样本），items = %d", len(page.Items))
	}
	if !page.HasMore {
		t.Error("首轮后仍有更多")
	}
	if page.SessionID == "" || !strings.HasPrefix(page.SessionID, catalogCursorPrefix) {
		t.Errorf("组合游标 = %q", page.SessionID)
	}

	// 第二轮：复用组合游标，各 genre offset 前进，漫剧已翻完只剩 2 个 genre
	page2, err := c.CatalogPage(context.Background(), CatalogQuery{Genre: CatalogGenreAll, Offset: page.Offset, SessionID: page.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Items) != 2 {
		t.Fatalf("第二轮应只剩 2 个 genre（漫剧翻完），items = %d", len(page2.Items))
	}
	for _, item := range page2.Items {
		if item.SeriesID == "" {
			t.Errorf("item = %+v", item)
		}
	}
	if !page2.HasMore {
		t.Error("仍应 has_more（真人/AI 未翻完）")
	}
}

func TestCatalogAllExhausted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(catalogLandpageFixture("", false, "", catalogCard("700001", "唯一")))
	}))
	defer srv.Close()
	c := NewClient()
	c.BaseURL = srv.URL
	c.HTTP = srv.Client()
	page, err := c.CatalogPage(context.Background(), CatalogQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 { // 三个 genre 同一剧，合并页内按 ID 去重
		t.Fatalf("items = %d, want 1", len(page.Items))
	}
	if page.HasMore {
		t.Error("全部翻完应 has_more=false")
	}
}

// ---- 分页防御规则 ----

func TestCatalogCursorDefense(t *testing.T) {
	cases := []struct {
		name    string
		body    map[string]any
		wantErr string
	}{
		{"next 不前进", catalogLandpageFixture("0", true, "s"), "未前进"},
		{"next 超界", catalogLandpageFixture("2000000", true, "s"), "未前进"},
		{"session 带换行", catalogLandpageFixture("18", true, "bad\r\nsession", catalogCard("700001", "x")), "会话"},
		{"next 非法但已到末页", catalogLandpageFixture("abc", false, "", catalogCard("700001", "x")), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(tc.body)
			}))
			defer srv.Close()
			c := NewClient()
			c.BaseURL = srv.URL
			c.HTTP = srv.Client()
			page, err := c.CatalogPage(context.Background(), CatalogQuery{Genre: CatalogGenreShortPlay})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			// has_more=false 且解析失败：next = offset + len(rows)
			if page.Offset != 1 {
				t.Errorf("offset = %d, want 1（offset+len(rows) 兜底）", page.Offset)
			}
		})
	}
}

func TestCatalogRetriesWithEmptySession(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		if payload["session_id"] == "stale" {
			atomic.AddInt32(&attempts, 1)
			w.WriteHeader(http.StatusForbidden) // 4xx 快速失败，避免 appRequest 内部重试拖慢测试
			return
		}
		next := strconv.Itoa(int(payload["offset"].(float64)) + 18)
		json.NewEncoder(w).Encode(catalogLandpageFixture(next, true, "fresh", catalogCard("700001", "x")))
	}))
	defer srv.Close()
	c := NewClient()
	c.BaseURL = srv.URL
	c.HTTP = srv.Client()
	page, err := c.CatalogPage(context.Background(), CatalogQuery{Genre: CatalogGenreAI, Offset: 18, SessionID: "stale"})
	if err != nil {
		t.Fatal(err)
	}
	if page.SessionID != "fresh" {
		t.Errorf("session = %q", page.SessionID)
	}
	if atomic.LoadInt32(&attempts) != 1 {
		t.Errorf("应先用原 session 失败一次再清空重试，attempts = %d", attempts)
	}
}
