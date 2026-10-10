package hongguo

// 搜索（双通道）+ 联想：对齐 guoapp-reference §3。
//   - 双通道 = 官网 SSR 搜索页 + 名称索引（联想端点 count=50 只取 short_play_name）
//   - 合并去重按 series_id，相关性排序 0-4（exact/prefix/contains/全分词包含/其他）
//   - 联想白名单 word_type 精确 5 值；防抖由前端做（server 不做）

import (
	"errors"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// searchText 排序/匹配用的文本归一（guoapp hongguoSearchText）：
// NFKC → 去空白+标点 → 小写。
func searchText(s string) string {
	s = norm.NFKC.String(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// searchKeyword 关键词校验（guoapp hongguoSearchKeyword）：
// NFKC + trim，1..80 字符，拒绝控制字符。
func searchKeyword(q string) (string, error) {
	q = strings.TrimSpace(norm.NFKC.String(q))
	if q == "" {
		return "", errors.New("关键词为空")
	}
	if n := len([]rune(q)); n > 80 {
		return "", errors.New("关键词超过 80 字")
	}
	for _, r := range q {
		if unicode.IsControl(r) {
			return "", errors.New("关键词含控制字符")
		}
	}
	return q, nil
}
