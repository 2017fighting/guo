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
