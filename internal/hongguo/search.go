package hongguo

// 搜索（双通道）+ 联想：对齐 guoapp-reference §3。
//   - 双通道 = 官网 SSR 搜索页 + 名称索引（联想端点 count=50 只取 short_play_name）
//   - 合并去重按 series_id，相关性排序 0-4（exact/prefix/contains/全分词包含/其他）
//   - 联想白名单 word_type 精确 5 值；防抖由前端做（server 不做）

import (
	"errors"
	"sort"
	"strconv"
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

// titleSearchRank 标题相关性（guoapp hongguoTitleSearchRank）：
// 0=归一后相等 1=前缀 2=包含 3=查询分词逐词全包含 4=其他。
func titleSearchRank(title, query string) int {
	t := searchText(title)
	q := searchText(query)
	switch {
	case t == q:
		return 0
	case strings.HasPrefix(t, q):
		return 1
	case strings.Contains(t, q):
		return 2
	}
	for _, tok := range searchQueryTokens(query) {
		if !strings.Contains(t, tok) {
			return 4
		}
	}
	return 3
}

// searchQueryTokens 查询分词：NFKC 后按空白/标点切，逐词小写。
func searchQueryTokens(query string) []string {
	normalized := norm.NFKC.String(query)
	tokens := strings.FieldsFunc(normalized, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	})
	for i, tok := range tokens {
		tokens[i] = strings.ToLower(tok)
	}
	return tokens
}

// rankSearchItems 按相关性升序排序；同 rank 同系列同 unit（季/部）时季号升序
// （guoapp tie-break），其余保持稳定（双通道合并后的原始顺序）。
func rankSearchItems(items []CatalogItem, query string) []CatalogItem {
	type ranked struct {
		item     CatalogItem
		rank     int
		base     string
		unit     string
		num      int
		hasSeson bool
	}
	rows := make([]ranked, len(items))
	for i, it := range items {
		base, unit, num, ok := seasonSuffix(it.Title)
		rows[i] = ranked{item: it, rank: titleSearchRank(it.Title, query), base: base, unit: unit, num: num, hasSeson: ok}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].rank != rows[j].rank {
			return rows[i].rank < rows[j].rank
		}
		a, b := rows[i], rows[j]
		if a.hasSeson && b.hasSeson && a.base == b.base && a.unit == b.unit {
			return a.num < b.num
		}
		return false
	})
	out := make([]CatalogItem, len(rows))
	for i, r := range rows {
		out[i] = r.item
	}
	return out
}

// seasonSuffix 识别标题尾的第N季/第N部（中文数字 + 阿拉伯数字）。
// base 为去掉后缀的标题；非结尾后缀或季号解析失败时不识别。
func seasonSuffix(title string) (base, unit string, num int, ok bool) {
	runes := []rune(strings.TrimSpace(title))
	if len(runes) < 3 {
		return title, "", 0, false
	}
	tail := runes[len(runes)-1]
	if tail != '季' && tail != '部' {
		return title, "", 0, false
	}
	for i := len(runes) - 2; i >= 0; i-- {
		if runes[i] == '第' {
			seg := string(runes[i+1 : len(runes)-1])
			n, err := parseSeasonNumber(seg)
			base = strings.TrimSpace(string(runes[:i]))
			if err != nil || base == "" {
				break
			}
			return base, string(tail), n, true
		}
		if !isSeasonNumeral(runes[i]) {
			break
		}
	}
	return title, "", 0, false
}

func isSeasonNumeral(r rune) bool {
	switch r {
	case '零', '〇', '一', '二', '两', '三', '四', '五', '六', '七', '八', '九', '十', '百':
		return true
	}
	return r >= '0' && r <= '9'
}

// parseSeasonNumber 季号：纯阿拉伯数字直转；中文数字按 十/百 权位累加。
func parseSeasonNumber(seg string) (int, error) {
	if seg == "" {
		return 0, errors.New("空季号")
	}
	if isASCIIDigits(seg) {
		return strconv.Atoi(seg)
	}
	total, cur := 0, 0
	for _, r := range seg {
		switch r {
		case '零', '〇':
			continue
		case '十':
			total += maxInt(cur, 1) * 10
			cur = 0
		case '百':
			total += maxInt(cur, 1) * 100
			cur = 0
		default:
			d, ok := chineseDigit(r)
			if !ok {
				return 0, errors.New("非法季号")
			}
			cur = d
		}
	}
	total += cur
	if total <= 0 || total > 9999 {
		return 0, errors.New("季号超出范围")
	}
	return total, nil
}

func chineseDigit(r rune) (int, bool) {
	switch r {
	case '一':
		return 1, true
	case '二', '两':
		return 2, true
	case '三':
		return 3, true
	case '四':
		return 4, true
	case '五':
		return 5, true
	case '六':
		return 6, true
	case '七':
		return 7, true
	case '八':
		return 8, true
	case '九':
		return 9, true
	}
	return 0, false
}

func isASCIIDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
