package hongguo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---- 搜索文本归一 + 关键词校验（guoapp-reference §3.1 hongguoSearchText / hongguoSearchKeyword） ----

func TestSearchTextNormalization(t *testing.T) {
	cases := []struct{ in, want string }{
		// NFKC：全角 ASCII → 半角
		{"Ｂａｉ月光", "bai月光"},
		// 去空白 + 标点（中西文都要去）
		{"宴律，你的白月光回国了", "宴律你的白月光回国了"},
		{"  白月光 ！？ ", "白月光"},
		{"宴 律·白月光", "宴律白月光"},
		// NFKC 兼容分解：带圈数字 ① → 1
		{"萌宝①号", "萌宝1号"},
		// 小写（NFKC 后再小写）
		{"Boss 白月光", "boss白月光"},
		// 全角空格 U+3000 也算空白
		{"白\u3000月光", "白月光"},
	}
	for _, tc := range cases {
		if got := searchText(tc.in); got != tc.want {
			t.Errorf("searchText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSearchKeywordValidation(t *testing.T) {
	// NFKC + trim 后合法
	kw, err := searchKeyword("  Ｂａｉ月光 ")
	if err != nil || kw != "Bai月光" {
		t.Fatalf("NFKC+trim: kw=%q err=%v", kw, err)
	}
	// 空 / 纯空白拒绝
	if _, err := searchKeyword("   "); err == nil {
		t.Fatal("空白关键词应被拒绝")
	}
	// 超过 80 字符拒绝
	if _, err := searchKeyword(strings.Repeat("剧", 81)); err == nil {
		t.Fatal("81 字关键词应被拒绝")
	}
	if _, err := searchKeyword(strings.Repeat("剧", 80)); err != nil {
		t.Fatalf("80 字关键词应通过: %v", err)
	}
	// 控制字符拒绝
	if _, err := searchKeyword("白月光\n回国"); err == nil {
		t.Fatal("含换行的关键词应被拒绝")
	}
}

// ---- 相关性排序（guoapp hongguoTitleSearchRank：0=精确 1=前缀 2=包含 3=分词全包含 4=其他） ----

func TestTitleSearchRank(t *testing.T) {
	cases := []struct {
		title string
		query string
		want  int
	}{
		// 0：归一后完全相等（全角/标点/大小写差异不算）
		{"白月光", "白月光", 0},
		{"Ｂａｉ月光！", "bai月光", 0},
		// 1：前缀
		{"白月光她不装了", "白月光", 1},
		// 2：包含（非前缀）
		{"宴律，你的白月光回国了", "白月光", 2},
		// 3：查询分词逐词全包含（词序无关）
		{"穿书后我成了反派的白月光", "白月光 穿书", 3},
		// 4：缺词 / 无关
		{"穿书后我成了反派的白月光", "白月光 逆袭", 4},
		{"霸总的千金妻", "白月光", 4},
		// 多词整体命中而未分词全命中时，先看 2（包含）再看 3：
		// “穿书 后我”连续出现在标题中 → 包含；但先做前缀/整串包含判断
		{"穿书后我成了反派的白月光", "穿书后我", 1},
	}
	for _, tc := range cases {
		if got := titleSearchRank(tc.title, tc.query); got != tc.want {
			t.Errorf("rank(%q, %q) = %d, want %d", tc.title, tc.query, got, tc.want)
		}
	}
}

func TestSearchRankSortsByRelevance(t *testing.T) {
	// 查询「月光 穿书」归一为「月光穿书」，前缀/包含均按整串判定：
	items := []CatalogItem{
		{SeriesID: "3", Title: "穿书后我成了反派的白月光"}, // rank 3（分词全包含，非连续）
		{SeriesID: "2", Title: "关于月光穿书这件事"},      // rank 2（包含）
		{SeriesID: "4", Title: "霸总的千金妻"},             // rank 4（其他）
		{SeriesID: "1", Title: "月光穿书之后"},             // rank 1（前缀）
		{SeriesID: "0", Title: "月光穿书"},                // rank 0（精确）
	}
	got := rankSearchItems(items, "月光 穿书")
	var order []string
	for _, it := range got {
		order = append(order, it.SeriesID)
	}
	want := []string{"0", "1", "2", "3", "4"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("排序 = %v, want %v", order, want)
		}
	}
}

func TestSearchRankSeasonTieBreak(t *testing.T) {
	// 同 rank 同系列同 unit（季）：季号升序（中文/阿拉伯数字都认）
	items := []CatalogItem{
		{SeriesID: "11", Title: "白月光第十季"},
		{SeriesID: "12", Title: "白月光第二季"},
		{SeriesID: "13", Title: "白月光第2季"},
	}
	got := rankSearchItems(items, "白月光")
	if got[0].SeriesID != "12" || got[1].SeriesID != "13" || got[2].SeriesID != "11" {
		var order []string
		for _, it := range got {
			order = append(order, it.Title)
		}
		t.Fatalf("季号 tie-break 顺序 = %v, want [第二季 第2季 第十季]", order)
	}
}

func TestSeasonSuffixParse(t *testing.T) {
	cases := []struct {
		title string
		base  string
		unit  string
		num   int
		ok    bool
	}{
		{title: "白月光第二季", base: "白月光", unit: "季", num: 2, ok: true},
		{title: "白月光第3部", base: "白月光", unit: "部", num: 3, ok: true},
		{title: "白月光第十季", base: "白月光", unit: "季", num: 10, ok: true},
		{title: "白月光第二十一季", base: "白月光", unit: "季", num: 21, ok: true},
		{title: "白月光第102季", base: "白月光", unit: "季", num: 102, ok: true},
		{title: "白月光", base: "白月光", unit: "", num: 0, ok: false},
		// 非结尾不识别
		{title: "第二季的白月光", base: "第二季的白月光", unit: "", num: 0, ok: false},
	}
	for _, tc := range cases {
		base, unit, num, ok := seasonSuffix(tc.title)
		if ok != tc.ok || base != tc.base || unit != tc.unit || num != tc.num {
			t.Errorf("seasonSuffix(%q) = (%q,%q,%d,%v), want (%q,%q,%d,%v)",
				tc.title, base, unit, num, ok, tc.base, tc.unit, tc.num, tc.ok)
		}
	}
}

// ---- 联想（guoapp-reference §3.2：incent_resource/suggestion，无签名，白名单 5 值） ----

// suggestRecordFixture 联想记录。
func suggestRecordFixture(name, wordType, seriesID string) map[string]any {
	rec := map[string]any{"name": name, "word_type": wordType, "keyword": name}
	if seriesID != "" {
		rec["video_data"] = map[string]any{
			"series_id_str": seriesID,
			"series_title":  name,
		}
	}
	return rec
}

func suggestResponse(records ...map[string]any) map[string]any {
	list := make([]any, 0, len(records))
	for _, r := range records {
		list = append(list, r)
	}
	return map[string]any{"suggest_list": list}
}

func newWebTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewClient()
	c.WebBaseURL = srv.URL
	c.HTTP = srv.Client()
	return c
}

func TestSuggestRequestAndFiltering(t *testing.T) {
	var gotPath, gotAppID, gotQuery, gotCount, gotReferer, gotAccept string
	c := newWebTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAppID = r.URL.Query().Get("app_id")
		gotQuery = r.URL.Query().Get("query")
		gotCount = r.URL.Query().Get("count")
		gotReferer = r.Header.Get("Referer")
		gotAccept = r.Header.Get("Accept")
		json.NewEncoder(w).Encode(suggestResponse(
			suggestRecordFixture("宴律，你的白月光回国了", "short_play_name", "700001"),
			suggestRecordFixture("白月光题材", "short_play_category", ""),
			suggestRecordFixture("某演员", "actor_name", ""),
			suggestRecordFixture("某剧演员", "short_play_actor", ""),
			suggestRecordFixture("大家都在搜白月光", "common_query", ""),
			// 白名单外：保留但 type 置空
			suggestRecordFixture("小说推荐", "book_name", ""),
			// 重名去重（忽略大小写）：BAI月光 先到，Bai月光 丢弃
			suggestRecordFixture("BAI月光", "common_query", ""),
			suggestRecordFixture("Bai月光", "actor_name", ""),
			// 名字过不了 1..80 校验 → 丢弃
			suggestRecordFixture(strings.Repeat("长", 81), "common_query", ""),
		))
	})

	items, err := c.Suggest(context.Background(), "白月光")
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if gotPath != "/incent_resource/suggestion" || gotAppID != "8662" || gotQuery != "白月光" || gotCount != "10" {
		t.Fatalf("请求参数不对: path=%s app_id=%s query=%s count=%s", gotPath, gotAppID, gotQuery, gotCount)
	}
	if !strings.HasPrefix(gotReferer, "http") || gotAccept != "application/json" {
		t.Fatalf("请求头不对: Referer=%q Accept=%q", gotReferer, gotAccept)
	}
	if len(items) != 7 {
		t.Fatalf("条目数 = %d, want 7", len(items))
	}
	byName := map[string]SuggestItem{}
	for _, it := range items {
		byName[it.Name] = it
	}
	if it := byName["宴律，你的白月光回国了"]; it.Type != "short_play_name" || it.SeriesID != "700001" {
		t.Fatalf("剧名联想应为白名单 type + series_id: %+v", it)
	}
	if it := byName["小说推荐"]; it.Type != "" {
		t.Fatalf("白名单外应保留但 type 置空: %+v", it)
	}
	if _, dup := byName["Bai月光"]; dup {
		t.Fatal("按 name 小写去重失败（白月光/Bai月光 应合并）")
	}
}

func TestSuggestDataEnvelope(t *testing.T) {
	// 两种响应形态都收：{"suggest_list":…} 与 {"data":{"suggest_list":…}}
	c := newWebTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"data": suggestResponse(suggestRecordFixture("白月光", "short_play_name", "700002")),
		})
	})
	items, err := c.Suggest(context.Background(), "白")
	if err != nil || len(items) != 1 || items[0].SeriesID != "700002" {
		t.Fatalf("data 信封形态解析失败: items=%+v err=%v", items, err)
	}
}

func TestSuggestCapTen(t *testing.T) {
	records := make([]map[string]any, 0, 15)
	for i := 0; i < 15; i++ {
		records = append(records, suggestRecordFixture(fmt.Sprintf("联想%d", i), "common_query", ""))
	}
	c := newWebTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(suggestResponse(records...))
	})
	items, err := c.Suggest(context.Background(), "联")
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(items) != 10 {
		t.Fatalf("条目数 = %d, want ≤10", len(items))
	}
}

func TestSuggestInvalidKeyword(t *testing.T) {
	c := NewClient()
	if _, err := c.Suggest(context.Background(), "  "); err == nil {
		t.Fatal("空关键词应报错")
	}
}
