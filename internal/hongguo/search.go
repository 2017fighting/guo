package hongguo

// 搜索（双通道）+ 联想：对齐 guoapp-reference §3。
//   - 双通道 = 官网 SSR 搜索页 + 名称索引（联想端点 count=50 只取 short_play_name）
//   - 合并去重按 series_id，相关性排序 0-4（exact/prefix/contains/全分词包含/其他）
//   - 联想白名单 word_type 精确 5 值；防抖由前端做（server 不做）

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
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

// ErrKeywordInvalid 关键词校验未过（空/超长/控制字符）。
// server 层据此映射 400，其余错误一律按上游故障处理。
var ErrKeywordInvalid = errors.New("关键词不合适")

// searchKeyword 关键词校验（guoapp hongguoSearchKeyword）：
// NFKC + trim，1..80 字符，拒绝控制字符。
func searchKeyword(q string) (string, error) {
	q = strings.TrimSpace(norm.NFKC.String(q))
	if q == "" {
		return "", fmt.Errorf("%w: 关键词为空", ErrKeywordInvalid)
	}
	if n := len([]rune(q)); n > 80 {
		return "", fmt.Errorf("%w: 关键词超过 80 字", ErrKeywordInvalid)
	}
	for _, r := range q {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%w: 关键词含控制字符", ErrKeywordInvalid)
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
		item      CatalogItem
		rank      int
		base      string
		unit      string
		num       int
		hasSeason bool
	}
	rows := make([]ranked, len(items))
	for i, it := range items {
		base, unit, num, ok := seasonSuffix(it.Title)
		rows[i] = ranked{item: it, rank: titleSearchRank(it.Title, query), base: base, unit: unit, num: num, hasSeason: ok}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].rank != rows[j].rank {
			return rows[i].rank < rows[j].rank
		}
		a, b := rows[i], rows[j]
		if a.hasSeason && b.hasSeason && a.base == b.base && a.unit == b.unit {
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

// ---- 联想（官网域 incent_resource/suggestion，无签名） ----

// SuggestItem 联想条目：name 展示；type 为白名单 word_type（白名单外置空）；
// 剧名联想带 series_id 可直达详情。
type SuggestItem struct {
	Name     string `json:"name"`
	Type     string `json:"type"`                // 白名单 word_type，白名单外为 ""
	SeriesID string `json:"series_id,omitempty"` // 有剧卡（video_data）时填
}

const (
	suggestAppID  = "8662"
	suggestLimit  = 10 // hongguoSuggestionLimit
	namesCount    = 50 // 名称索引通道的联想拉取量
	suggestBudget = 5 * time.Second
)

// suggestWhitelist 精确 5 值（guoapp fetchHongguoSearchSuggestions）。
var suggestWhitelist = map[string]bool{
	"short_play_name":     true,
	"short_play_category": true,
	"common_query":        true,
	"actor_name":          true,
	"short_play_actor":    true,
}

// webBase 官网域基址（可测试注入）。
func (c *Client) webBase() string {
	if c.WebBaseURL != "" {
		return c.WebBaseURL
	}
	return webBaseURL
}

// Suggest 联想（≤10 条）：白名单 word_type、name 去重（忽略大小写）、
// 过不了关键词校验的丢弃。防抖由前端负责，服务端不做。
func (c *Client) Suggest(ctx context.Context, query string) ([]SuggestItem, error) {
	kw, err := searchKeyword(query)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, suggestBudget)
	defer cancel()
	records, err := c.fetchSuggestionRecords(ctx, kw, suggestLimit)
	if err != nil {
		return nil, err
	}
	items := make([]SuggestItem, 0, len(records))
	seen := make(map[string]bool, len(records))
	for _, rec := range records {
		name := strings.TrimSpace(mapString(rec, "name"))
		if _, err := searchKeyword(name); err != nil {
			continue // name 过不了 1..80 校验 → 丢弃
		}
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		wordType := mapString(rec, "word_type")
		if !suggestWhitelist[wordType] {
			wordType = "" // 白名单外保留条目但 type 置空
		}
		item := SuggestItem{Name: name, Type: wordType}
		if card, ok := rec["video_data"].(map[string]any); ok {
			if id := mapString(card, "series_id_str", "series_id"); numericID.MatchString(id) {
				item.SeriesID = id
			}
		}
		items = append(items, item)
		if len(items) >= suggestLimit {
			break
		}
	}
	return items, nil
}

// fetchSuggestionRecords 拉联想端点原始记录；
// 响应兼容 {"suggest_list":…} 与 {"data":{"suggest_list":…}} 两种形态。
func (c *Client) fetchSuggestionRecords(ctx context.Context, query string, count int) ([]map[string]any, error) {
	u := c.webBase() + "/incent_resource/suggestion?" + url.Values{
		"app_id": {suggestAppID},
		"query":  {query},
		"count":  {strconv.Itoa(count)},
	}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", webUA)
	req.Header.Set("Referer", c.webBase()+"/")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("联想接口 HTTP %d", resp.StatusCode)
	}
	var result map[string]any
	dec := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes))
	dec.UseNumber()
	if err := dec.Decode(&result); err != nil {
		return nil, fmt.Errorf("联想响应解析失败: %w", err)
	}
	list, _ := result["suggest_list"].([]any)
	if list == nil {
		list, _ = nestedMap(result, "data")["suggest_list"].([]any)
	}
	return mapList(list), nil
}

// ---- 双通道搜索（官网 SSR 搜索页 + 名称索引） ----

// SearchResult 搜索响应：合并去重后的剧卡 + 降级警告。
type SearchResult struct {
	Query    string        `json:"query"`    // NFKC+trim 后的关键词回显
	Items    []CatalogItem `json:"items"`    // 相关性排序后的合并结果
	Limited  bool          `json:"limited"`  // 官网搜索页只返回了部分结果（totalCount > 返回条数）
	Warnings []string      `json:"warnings"` // 单通道降级提示（空数组 = 无）
}

const searchPageBudget = 12 * time.Second

// Search 双通道搜索：名称索引（联想端点 count=50 只取 short_play_name）+
// 官网 SSR 搜索页，按 series_id 合并去重后相关性排序。单通道失败降级带警告，
// 双通道全挂才报错。防抖由前端负责。
func (c *Client) Search(ctx context.Context, query string) (*SearchResult, error) {
	kw, err := searchKeyword(query)
	if err != nil {
		return nil, err
	}
	var (
		wg       sync.WaitGroup
		names    []CatalogItem
		namesErr error
		page     []CatalogItem
		limited  bool
		pageErr  error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		names, namesErr = c.searchNames(ctx, kw)
	}()
	go func() {
		defer wg.Done()
		page, limited, pageErr = c.searchPage(ctx, kw)
	}()
	wg.Wait()

	if namesErr != nil && pageErr != nil {
		return nil, fmt.Errorf("官网搜索页: %v；名称索引: %v", pageErr, namesErr)
	}
	warnings := make([]string, 0, 2)
	if pageErr != nil {
		warnings = append(warnings, "红果综合搜索暂不可用，已只显示名称索引结果")
	} else if namesErr != nil {
		warnings = append(warnings, "名称检索暂不可用，已只显示官网搜索结果")
	}
	merged := mergeCatalogItems(names, page)
	return &SearchResult{
		Query:    kw,
		Items:    rankSearchItems(merged, kw),
		Limited:  limited,
		Warnings: warnings,
	}, nil
}

// searchNames 名称索引通道：联想端点 count=50，只取 word_type==short_play_name
// 且剧卡 series_id 为数字的记录（video_data 即完整剧卡）。
func (c *Client) searchNames(ctx context.Context, kw string) ([]CatalogItem, error) {
	ctx, cancel := context.WithTimeout(ctx, suggestBudget)
	defer cancel()
	records, err := c.fetchSuggestionRecords(ctx, kw, namesCount)
	if err != nil {
		return nil, err
	}
	items := make([]CatalogItem, 0, len(records))
	for _, rec := range records {
		if mapString(rec, "word_type") != "short_play_name" {
			continue
		}
		if item, ok := catalogItemFromCard(rec); ok {
			items = append(items, item)
		}
	}
	return items, nil
}

// searchPage 官网 SSR 搜索页通道（无分页，一次全量）。
// 要求 isSuccess=true 且 query 回显一致；Limited = totalCount > 条目数。
func (c *Client) searchPage(ctx context.Context, kw string) ([]CatalogItem, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, searchPageBudget)
	defer cancel()
	body, err := c.fetchWeb(ctx, "/search/"+url.PathEscape(kw))
	if err != nil {
		return nil, false, err
	}
	page := routerLoaderMap(parseRouterData(body), "search_(keyword)/page", "search_")
	if !mapBool(page, "isSuccess") || mapString(page, "query") != kw {
		return nil, false, errors.New("红果综合搜索页返回异常")
	}
	rows := anyList(page["searchList"])
	items := make([]CatalogItem, 0, len(rows))
	for _, row := range rows {
		if item, ok := catalogItemFromCard(row); ok {
			items = append(items, item)
		}
	}
	total, _ := strconv.Atoi(mapString(page, "totalCount"))
	return items, total > len(items), nil
}

// mergeCatalogItems 按 series_id 合并去重：后到的字段只补已有卡的空缺，
// 新剧追加在后（guoapp mergeHongguoSearchDramas）。
func mergeCatalogItems(first, later []CatalogItem) []CatalogItem {
	merged := make([]CatalogItem, 0, len(first)+len(later))
	merged = append(merged, first...)
	index := make(map[string]int, len(merged))
	for i, it := range merged {
		index[it.SeriesID] = i
	}
	for _, it := range later {
		if j, ok := index[it.SeriesID]; ok {
			merged[j] = mergeCatalogItem(merged[j], it)
			continue
		}
		index[it.SeriesID] = len(merged)
		merged = append(merged, it)
	}
	return merged
}

// mergeCatalogItem 后到的卡 merge 进已有卡：逐字段补空缺，不覆盖已有值。
func mergeCatalogItem(a, b CatalogItem) CatalogItem {
	if a.Title == "" {
		a.Title = b.Title
	}
	if a.Cover == "" {
		a.Cover = b.Cover
	}
	if a.EpisodeCount == "" {
		a.EpisodeCount = b.EpisodeCount
	}
	if a.Status == "" {
		a.Status = b.Status
	}
	if a.Heat == "" {
		a.Heat = b.Heat
	}
	if a.PlayCount == "" {
		a.PlayCount = b.PlayCount
	}
	if a.OnlineDate == "" {
		a.OnlineDate = b.OnlineDate
	}
	if !a.Vertical {
		a.Vertical = b.Vertical
	}
	if !a.VIP {
		a.VIP = b.VIP
	}
	if a.Category == "" {
		a.Category = b.Category
	}
	if len(a.Tags) == 0 {
		a.Tags = b.Tags
	}
	return a
}
