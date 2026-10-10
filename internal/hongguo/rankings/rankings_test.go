package rankings

// api3 榜单客户端测试：httptest 假 api3（plan + cell/change），覆盖
// 首页换榜请求形态、翻页游标（next_offset+session_id+filter_ids+rank_version）、
// 响应解析（抓包样本回放 + 真机 list 形态）、防御规则。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

const fakeCellID = "7470092475068071998"

// fakeAPI3 模拟 api3 两个端点。planBody 由回调生成（默认自带 cell_selector 分类）；
// cell/change 按 (sub_selected_items, offset) 分发页面。
type fakeAPI3 struct {
	t          *testing.T
	planHits   atomic.Int32
	changeHits atomic.Int32
	lastQuery  url.Values

	pages map[string][]string // board id -> 逐页 JSON 响应
	plan  func(query url.Values) string
}

func newFakeAPI3(t *testing.T) *fakeAPI3 {
	t.Helper()
	return &fakeAPI3{t: t, pages: map[string][]string{}}
}

func (f *fakeAPI3) setPlan(body string) { f.plan = func(url.Values) string { return body } }

func (f *fakeAPI3) addBoard(boardID string, pages ...string) { f.pages[boardID] = pages }

func (f *fakeAPI3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.lastQuery = r.URL.Query()
	body := ""
	switch {
	case strings.HasPrefix(r.URL.Path, "/reading/bookapi/plan/"):
		f.planHits.Add(1)
		if f.plan == nil {
			http.Error(w, "plan not configured", 500)
			return
		}
		body = f.plan(f.lastQuery)
		f.checkCommonParams(w, r)
	case strings.HasPrefix(r.URL.Path, "/reading/bookapi/bookmall/cell/change/"):
		f.changeHits.Add(1)
		board := f.lastQuery.Get("sub_selected_items")
		offset, _ := strconv.Atoi(f.lastQuery.Get("offset"))
		pages := f.pages[board]
		if offset/10 >= len(pages) || offset%10 != 0 || offset < 0 {
			http.Error(w, fmt.Sprintf("no page for %s@%d", board, offset), 500)
			return
		}
		body = pages[offset/10]
		f.checkCommonParams(w, r)
	default:
		http.Error(w, "unexpected path "+r.URL.Path, 500)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

// checkCommonParams 设备参数尾串 + 签名头校验（抓包口径，guoapp-reference §5.2）。
func (f *fakeAPI3) checkCommonParams(w http.ResponseWriter, r *http.Request) {
	q := f.lastQuery
	for _, key := range []string{"aid", "app_name", "version_code", "device_id", "iid", "_rticket", "session_uuid"} {
		if q.Get(key) == "" {
			http.Error(w, "missing param "+key, 500)
			return
		}
	}
	if q.Get("version_code") != "73970" {
		http.Error(w, "version_code should be 73970", 500)
		return
	}
	if q.Get("device_type") != "sdk_gphone64_arm64" {
		http.Error(w, "device_type should match capture", 500)
		return
	}
	if !strings.Contains(r.Header.Get("User-Agent"), "com.phoenix.read/73970") {
		http.Error(w, "UA should be 73970 cronet", 500)
		return
	}
	// 轻量 Gorgon 套件；短 Argus/Ladon 形态带错会被真机静默拒绝（实测），一律不带
	for _, header := range []string{"X-Gorgon", "X-Khronos", "X-SS-Req-Ticket"} {
		if r.Header.Get(header) == "" {
			http.Error(w, "missing signing header "+header, 500)
			return
		}
	}
	for _, header := range []string{"X-Argus", "X-Ladon", "X-Helios", "X-Medusa"} {
		if r.Header.Get(header) != "" {
			http.Error(w, "opaque header "+header+" must be omitted", 500)
			return
		}
	}
}

func newTestClient(t *testing.T, fake *fakeAPI3) *Client {
	t.Helper()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	c := NewClient()
	c.BaseURL = server.URL
	c.HTTP = server.Client()
	return c
}

// boardPageBody 构造 cell/change 响应（抓包 map 形态：data.cell_view.cell_data）。
func boardPageBody(items []map[string]any, next int, hasMore bool, session, rankVersion string) string {
	cells := make([]any, 0, len(items))
	for _, item := range items {
		cells = append(cells, map[string]any{"video_data": []any{item}})
	}
	return marshal(map[string]any{
		"code": 0, "message": "SUCCESS",
		"data": map[string]any{
			"cell_view": map[string]any{"cell_data": cells, "show_type": 505},
			"next_offset": strconv.Itoa(next), "has_more": hasMore,
			"session_id": session, "rank_version": rankVersion,
		},
	})
}

func rankItem(id, title, rank, heat string) map[string]any {
	return map[string]any{
		"series_id": id, "title": title, "cover": "//p3.example.test/" + id + ".jpg",
		"video_desc": title + " 的简介", "vertical": true, "episode_cnt": json.Number("84"),
		"play_cnt": json.Number("2218691"), "score": json.Number("8.1"),
		"recommend_info": `{"rank":"` + rank + `","gid":"1"}`,
		"secondary_info_list": []any{map[string]any{"content": heat, "data_type": 1}},
	}
}

func marshal(v any) string {
	content, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(content)
}

// fakePlanBody 精简版 plan 响应：list 形态（真机实测 data 为数组，首个元素即榜单 cell），
// cell_selector 提供外层 tab → 榜单分类（抓包 taxonomy 的最小子集）。
func fakePlanBody(defaultBoardFirstPage string) string {
	sub := func(ids ...string) []any {
		items := make([]any, 0, len(ids))
		for _, id := range ids {
			items = append(items, map[string]any{"selector_item_id": id, "show_name": id})
		}
		return items
	}
	first := map[string]any{}
	_ = json.Unmarshal([]byte(defaultBoardFirstPage), &first)
	dataPage, _ := first["data"].(map[string]any)
	element := map[string]any{
		"cell_id": fakeCellID, "cell_id_str": fakeCellID, "show_type": 505,
		"cell_selector": map[string]any{
			"outer_row": map[string]any{"type": "outer_row", "selection_type": 3, "items": []any{
				map[string]any{"selector_item_id": "all", "show_name": "全部", "sub_cell_selector": map[string]any{
					"outer_row": map[string]any{"items": sub("ranklist_hot_sc", "ranklist_prestige", "human_hot_play")}}},
				map[string]any{"selector_item_id": "human", "show_name": "真人剧", "sub_cell_selector": map[string]any{
					"outer_row": map[string]any{"items": sub("human_hot_sc", "human_hot_play")}}},
				map[string]any{"selector_item_id": "ai_playlet", "show_name": "AI剧", "sub_cell_selector": map[string]any{
					"outer_row": map[string]any{"items": sub("ai_playlet_hot_sc")}}},
			}},
		},
	}
	for _, key := range []string{"cell_data", "next_offset", "has_more", "session_id"} {
		if v, ok := dataPage["cell_view"].(map[string]any)[key]; ok {
			element[key] = v
		}
	}
	return marshal(map[string]any{"code": 0, "message": "SUCCESS", "data": []any{element}})
}

func TestBoardFirstPageSwitchesBoardViaCellChange(t *testing.T) {
	fake := newFakeAPI3(t)
	fake.setPlan(fakePlanBody(boardPageBody(nil, 0, false, "plan-sess", "")))
	fake.addBoard("ranklist_prestige",
		boardPageBody([]map[string]any{rankItem("7101", "先斩意中人", "1", "1.6万人评分")}, 10, true, "sess-a", "1791603600"))
	c := newTestClient(t, fake)

	page, err := c.Board(context.Background(), "ranklist_prestige", 0, "")
	if err != nil {
		t.Fatalf("首页换榜失败: %v", err)
	}
	if got := fake.changeHits.Load(); got != 1 {
		t.Fatalf("应发起一次 cell/change，实际 %d", got)
	}
	q := fake.lastQuery
	if q.Get("cell_id") != fakeCellID {
		t.Fatalf("cell_id 应取自 plan 响应，实际 %q", q.Get("cell_id"))
	}
	if q.Get("sub_selected_items") != "ranklist_prestige" {
		t.Fatalf("sub_selected_items 应为榜单 id，实际 %q", q.Get("sub_selected_items"))
	}
	if q.Get("selected_items") != "all" {
		t.Fatalf("selected_items 应为外层 tab all（plan taxonomy 推导），实际 %q", q.Get("selected_items"))
	}
	if q.Get("offset") != "0" {
		t.Fatalf("首页 offset 应为 0，实际 %q", q.Get("offset"))
	}
	if q.Get("unlimited_selector_change_type") != "2" {
		t.Fatalf("首页换榜 unlimited_selector_change_type 应为 2，实际 %q", q.Get("unlimited_selector_change_type"))
	}
	if q.Get("session_id") != "" || q.Get("filter_ids") != "" {
		t.Fatalf("首页不应带游标 session/filter，实际 %q/%q", q.Get("session_id"), q.Get("filter_ids"))
	}
	if len(page.Items) != 1 || page.Items[0].Title != "先斩意中人" || page.Items[0].Rank != 1 {
		t.Fatalf("条目解析不符: %+v", page.Items)
	}
	if page.Items[0].Heat != "1.6万人评分" || page.Items[0].EpisodeCount != "84" || page.Items[0].PlayCount != "2218691" {
		t.Fatalf("热度/集数/播放量解析不符: %+v", page.Items[0])
	}
	if !strings.HasPrefix(page.Items[0].Cover, "https://") {
		t.Fatalf("封面应补 https 前缀: %q", page.Items[0].Cover)
	}
	if page.Offset != 10 || !page.HasMore || page.SessionID != "sess-a" || page.RankVersion != "1791603600" {
		t.Fatalf("游标解析不符: %+v", page)
	}
	if len(page.FilterIDs) != 1 || page.FilterIDs[0] != "7101" {
		t.Fatalf("filter_ids 应为本页 series_id: %v", page.FilterIDs)
	}
}

func TestBoardPaginationThreadsCursor(t *testing.T) {
	fake := newFakeAPI3(t)
	fake.setPlan(fakePlanBody(boardPageBody(nil, 0, false, "plan-sess", "")))
	var page1IDs []map[string]any
	for i := 1; i <= 10; i++ {
		page1IDs = append(page1IDs, rankItem(fmt.Sprintf("710%d", i), fmt.Sprintf("剧%d", i), strconv.Itoa(i), "100万热度"))
	}
	fake.addBoard("ranklist_hot_sc",
		boardPageBody(page1IDs, 10, true, "sess-1", "1791603600"),
		boardPageBody([]map[string]any{rankItem("7201", "第二页剧", "11", "50万热度")}, 20, false, "sess-2", "1791603600"))
	c := newTestClient(t, fake)

	first, err := c.Board(context.Background(), "ranklist_hot_sc", 0, "")
	if err != nil {
		t.Fatalf("首页失败: %v", err)
	}
	cursor := EncodeCursor(Cursor{SessionID: first.SessionID, FilterIDs: first.FilterIDs, RankVersion: first.RankVersion})
	second, err := c.Board(context.Background(), "ranklist_hot_sc", first.Offset, cursor)
	if err != nil {
		t.Fatalf("翻页失败: %v", err)
	}
	q := fake.lastQuery
	if q.Get("offset") != "10" {
		t.Fatalf("翻页 offset 应为 10，实际 %q", q.Get("offset"))
	}
	if q.Get("session_id") != "sess-1" {
		t.Fatalf("翻页应回传 session_id，实际 %q", q.Get("session_id"))
	}
	if got := q.Get("filter_ids"); got != strings.Join(first.FilterIDs, ",") {
		t.Fatalf("翻页 filter_ids 应为上一页 series_id 逗号串，实际 %q", got)
	}
	if q.Get("rank_version") != "1791603600" {
		t.Fatalf("翻页应回传 rank_version，实际 %q", q.Get("rank_version"))
	}
	if q.Get("unlimited_selector_change_type") != "1" {
		t.Fatalf("翻页 unlimited_selector_change_type 应为 1，实际 %q", q.Get("unlimited_selector_change_type"))
	}
	if len(second.Items) != 1 || second.HasMore || second.Offset != 20 {
		t.Fatalf("第二页解析不符: %+v", second)
	}
}

func TestBoardRejectsInvalidList(t *testing.T) {
	fake := newFakeAPI3(t)
	fake.setPlan(fakePlanBody(boardPageBody(nil, 0, false, "s", "")))
	c := newTestClient(t, fake)
	if _, err := c.Board(context.Background(), "no_such_board", 0, ""); err == nil {
		t.Fatal("未知榜单应报错")
	}
	if fake.changeHits.Load() != 0 {
		t.Fatal("未知榜单不应发起 cell/change 请求")
	}
}

func TestBoardOffsetRequiresCursor(t *testing.T) {
	fake := newFakeAPI3(t)
	fake.setPlan(fakePlanBody(boardPageBody(nil, 0, false, "s", "")))
	c := newTestClient(t, fake)
	if _, err := c.Board(context.Background(), "ranklist_hot_sc", 10, ""); err == nil {
		t.Fatal("offset>0 缺游标应报错")
	}
	if fake.changeHits.Load() != 0 {
		t.Fatal("缺游标不应发起请求")
	}
}

func TestBoardDefendsNonAdvancingOffset(t *testing.T) {
	fake := newFakeAPI3(t)
	fake.setPlan(fakePlanBody(boardPageBody(nil, 0, false, "s", "")))
	fake.addBoard("ranklist_hot_sc",
		boardPageBody([]map[string]any{rankItem("7101", "剧", "1", "热度")}, 10, true, "sess-1", ""),
		boardPageBody([]map[string]any{rankItem("7201", "剧2", "11", "热度")}, 10, true, "sess-2", "")) // next 未前进
	c := newTestClient(t, fake)
	first, err := c.Board(context.Background(), "ranklist_hot_sc", 0, "")
	if err != nil {
		t.Fatalf("首页失败: %v", err)
	}
	cursor := EncodeCursor(Cursor{SessionID: first.SessionID, FilterIDs: first.FilterIDs})
	if _, err := c.Board(context.Background(), "ranklist_hot_sc", 10, cursor); err == nil {
		t.Fatal("next_offset 未前进应报错")
	}
}

func TestBoardEmptyItemsError(t *testing.T) {
	fake := newFakeAPI3(t)
	fake.setPlan(fakePlanBody(boardPageBody(nil, 0, false, "s", "")))
	fake.addBoard("ranklist_hot_sc", boardPageBody(nil, 10, true, "sess-x", ""))
	c := newTestClient(t, fake)
	if _, err := c.Board(context.Background(), "ranklist_hot_sc", 0, ""); err == nil {
		t.Fatal("空 cell_data 应报错（榜单暂无剧集）")
	}
}

func TestBoardsTaxonomyFromPlanWithFallback(t *testing.T) {
	fake := newFakeAPI3(t)
	fake.setPlan(fakePlanBody(boardPageBody(nil, 0, false, "s", "")))
	c := newTestClient(t, fake)

	boards, err := c.Boards(context.Background())
	if err != nil {
		t.Fatalf("Boards 失败: %v", err)
	}
	var prestige *BoardInfo
	for i, b := range boards {
		if b.ID == "ranklist_prestige" {
			prestige = &boards[i]
		}
	}
	if prestige == nil || prestige.Tab != "all" {
		t.Fatalf("taxonomy 应给出 ranklist_prestige 的外层 tab=all，实际 %+v", prestige)
	}

	// plan 不可达时回退静态映射（8 榜仍可用）
	c2 := newTestClient(t, &fakeAPI3{t: t})
	boards2, err := c2.Boards(context.Background())
	if err != nil {
		t.Fatalf("plan 失败应回退静态 8 榜: %v", err)
	}
	if len(boards2) != len(Boards) {
		t.Fatalf("静态回退应为 8 榜，实际 %d", len(boards2))
	}
	for _, b := range boards2 {
		if b.Tab == "" {
			t.Fatalf("静态映射缺少 tab: %+v", b)
		}
	}
}

// TestParseReplayBodies 回放抓包样本（docs/research/raw/bodies），锁定真机响应 schema。
func TestParseReplayBodies(t *testing.T) {
	sample := filepath.Join("..", "..", "..", "docs", "research", "raw", "bodies")
	entries, err := os.ReadDir(sample)
	if err != nil {
		t.Skipf("样本目录不可读: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if name != "plan.json" && name != "ranklist_hot_sc.json" && name != "human_hot_play.json" {
			continue
		}
		content, err := os.ReadFile(filepath.Join(sample, name))
		if err != nil {
			t.Fatalf("读取样本 %s 失败: %v", name, err)
		}
		page, err := parseBoardResponse(content, 0)
		if err != nil {
			t.Fatalf("样本 %s 解析失败: %v", name, err)
		}
		if len(page.Items) == 0 {
			t.Fatalf("样本 %s 未解析出条目", name)
		}
		first := page.Items[0]
		if first.SeriesID == "" || first.Title == "" || first.Rank != 1 {
			t.Fatalf("样本 %s 首条目异常: %+v", name, first)
		}
		if first.Heat == "" {
			t.Fatalf("样本 %s 首条目缺热度文本", name)
		}
		t.Logf("%s: %d 条，首条 %s(rank=%d heat=%s)", name, len(page.Items), first.Title, first.Rank, first.Heat)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	in := Cursor{SessionID: "20261010F74DC8F84B67EDA15E85", FilterIDs: []string{"7101", "7102"}, RankVersion: "1791603600"}
	encoded := EncodeCursor(in)
	if !strings.HasPrefix(encoded, cursorPrefix) {
		t.Fatalf("组合游标应带 %q 前缀，实际 %q", cursorPrefix, encoded)
	}
	out, ok := DecodeCursor(encoded)
	if !ok || out.SessionID != in.SessionID || strings.Join(out.FilterIDs, ",") != "7101,7102" || out.RankVersion != in.RankVersion {
		t.Fatalf("游标往返不符: %+v", out)
	}
	if _, ok := DecodeCursor(""); ok {
		t.Fatal("空游标不可解码")
	}
	if _, ok := DecodeCursor("garbage"); ok {
		t.Fatal("乱游标不可解码")
	}
	if _, ok := DecodeCursor("m.abc"); ok {
		t.Fatal("其他前缀游标不可解码")
	}
}
