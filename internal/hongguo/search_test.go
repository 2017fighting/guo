package hongguo

import (
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
