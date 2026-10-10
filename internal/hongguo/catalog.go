package hongguo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 目录 feed（浏览页）：App 分类 landpage 通道，对齐 guoapp-reference §1.1。
// guoapp 恒 need_selector_panel=false；筛选面板枚举见 CatalogFilters（need_selector_panel=true 探测 + 兜底）。

const catalogPageSize = 18

// genre 枚举（/api/v1/catalog 的 genre 参数；all = 三 genre 轮询合并，guoapp fetchHongguoAppCatalogCategory(ctx, "")）。
const (
	CatalogGenreAll       = "all"
	CatalogGenreShortPlay = "short_play"
	CatalogGenreComic     = "comic_series"
	CatalogGenreAI        = "ai_series"
)

// catalogGenres 单 genre 通道的 req_scene 映射（guoapp hongguoAppGenres）。
var catalogGenres = []struct{ Key, Scene, Name string }{
	{"short_play", "default", "真人剧"},
	{"comic_series", "comic_series", "漫剧"},
	{"ai_series", "ai_series", "AI剧"},
}

// CatalogQuery 目录页请求；游标来自上一页 CatalogPage 的 Offset/SessionID。
type CatalogQuery struct {
	Genre     string
	Offset    int
	SessionID string
}

// CatalogItem 目录卡片：浏览页展示 + 客户端排序/过滤所需字段
// （热度/播放量保留原始文本，前端按 guoapp catalogMetric 口径解析）。
type CatalogItem struct {
	SeriesID     string   `json:"series_id"`
	Title        string   `json:"title"`
	Cover        string   `json:"cover"`
	EpisodeCount string   `json:"episode_count"`
	Status       string   `json:"status"`      // "完结"|"连载"|""
	Heat         string   `json:"heat"`        // 热度文本（hot_score_data.score 优先）
	PlayCount    string   `json:"play_count"`  // 播放量文本
	OnlineDate   string   `json:"online_date"` // "2006-01-02" 或 ""
	Vertical     bool     `json:"vertical"`
	VIP          bool     `json:"vip,omitempty"`
	Category     string   `json:"category,omitempty"`
	Tags         []string `json:"tags,omitempty"`
}

// CatalogPage 目录页结果 + 下一页游标（HasMore=false 时为末页）。
type CatalogPage struct {
	Items     []CatalogItem `json:"items"`
	Offset    int           `json:"offset"`
	SessionID string        `json:"session_id"`
	HasMore   bool          `json:"has_more"`
}

// CatalogPage 拉取一页目录。genre=all 时对三个 genre 各取一页合并（≈54 条），
// SessionID 为内部组合游标（前缀区分，对上层透明）。
func (c *Client) CatalogPage(ctx context.Context, q CatalogQuery) (*CatalogPage, error) {
	if q.Genre == "" {
		q.Genre = CatalogGenreAll
	}
	if q.Genre != CatalogGenreAll && !catalogGenreValid(q.Genre) {
		return nil, errors.New("红果分类无效（可用：all/short_play/comic_series/ai_series）")
	}
	if q.Offset < 0 {
		return nil, errors.New("目录游标 offset 不能为负")
	}
	if q.Genre == CatalogGenreAll {
		return c.catalogAllPage(ctx, q)
	}
	return c.catalogGenrePage(ctx, q.Genre, q.Offset, q.SessionID)
}

func catalogGenreValid(genre string) bool {
	for _, g := range catalogGenres {
		if g.Key == genre {
			return true
		}
	}
	return false
}

func catalogGenreScene(genre string) string {
	for _, g := range catalogGenres {
		if g.Key == genre {
			return g.Scene
		}
	}
	return ""
}

// catalogGenrePage 单 genre 一页。请求失败且带 session 时，按 guoapp 口径
// 用空 session 重试一次（会话过期服务端会拒绝续翻）。
func (c *Client) catalogGenrePage(ctx context.Context, genre string, offset int, session string) (*CatalogPage, error) {
	payload := catalogLandpagePayload(genre, offset, session)
	result, err := c.appRequest(ctx, "POST", "/reading/distribution/category/landpage/v/", nil, payload, false)
	if err != nil && session != "" && ctx.Err() == nil {
		payload = catalogLandpagePayload(genre, offset, "")
		result, err = c.appRequest(ctx, "POST", "/reading/distribution/category/landpage/v/", nil, payload, false)
	}
	if err != nil {
		return nil, err
	}
	return parseCatalogPage(result, offset)
}

func catalogLandpagePayload(genre string, offset int, session string) map[string]any {
	reqType := 3
	if offset > 0 {
		reqType = 2
	}
	return map[string]any{
		"req_scene": catalogGenreScene(genre), "offset": offset, "limit": catalogPageSize,
		"req_type": "only_content", "need_selector_panel": false, "client_req_type": reqType,
		"session_id": session, "filter_ids": "",
		"select_items": map[string]any{
			"genre": []string{genre}, "sort": []string{"online_time"}, "gender": []string{},
			"category_dim_theme": []string{}, "category_dim_role": []string{}, "category_dim_epoch": []string{},
			"online_time": []string{}, "creation_status": []string{},
		},
	}
}

// parseCatalogPage 解析 landpage 响应 + 分页防御规则（guoapp parseHongguoCatalogPage）：
// next_offset 必须严格前进、>1e6 非法、has_more=false 且解析失败时按 offset+len 兜底、
// session_id ≤4096 且不含控制字符。页签名防重放规则依赖跨请求记忆，本服务为无游标状态
// 的转发层（游标在客户端），故不适用——严格前进规则已挡住原地分页。
func parseCatalogPage(result map[string]any, offset int) (*CatalogPage, error) {
	data := nestedMap(result, "data")
	rows, ok := data["video_data"].([]any)
	if !ok {
		return nil, errors.New("App 分类数据格式异常")
	}
	items := make([]CatalogItem, 0, len(rows))
	for _, row := range rows {
		if item, ok := catalogItemFromCard(row); ok {
			items = append(items, item)
		}
	}
	if len(rows) > 0 && len(items) == 0 {
		return nil, errors.New("App 分类未返回可识别的剧集")
	}
	next, parseErr := strconv.Atoi(mapString(data, "next_offset"))
	hasMore, hasMoreOK := data["has_more"].(bool)
	if !hasMoreOK || hasMore && parseErr != nil {
		return nil, errors.New("App 分页标记无效，请稍后重试")
	}
	if parseErr != nil {
		next = offset + len(rows)
	}
	if hasMore && (len(items) == 0 || next <= offset || next > 1_000_000) {
		return nil, errors.New("App 分页未前进，请稍后重试")
	}
	if !hasMore && (next < offset || next > 1_000_000) {
		next = offset
	}
	session := mapString(data, "session_id")
	if len(session) > 4096 || strings.ContainsAny(session, "\r\n\x00") {
		return nil, errors.New("App 分页会话无效，请稍后重试")
	}
	return &CatalogPage{Items: items, Offset: next, SessionID: session, HasMore: hasMore}, nil
}

var catalogNumericID = regexp.MustCompile(`^[0-9]{1,32}$`)

var chinaTimeZone = time.FixedZone("CST", 8*3600)

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// catalogItemFromCard 从 landpage/rankings 两族剧卡取规范化字段
// （键别名对齐 guoapp hongguoDramaFromAny）。ID 非数字整卡丢弃。
func catalogItemFromCard(v any) (CatalogItem, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return CatalogItem{}, false
	}
	card, _ := m["video_data"].(map[string]any)
	if len(card) == 0 {
		card = m
	}
	id := firstNonEmpty(mapString(card, "series_id_str", "series_id"), mapString(m, "series_id_str", "series_id"))
	if !catalogNumericID.MatchString(id) {
		return CatalogItem{}, false
	}
	item := CatalogItem{
		SeriesID:     id,
		Title:        firstNonEmpty(mapString(card, "series_title", "series_name", "title"), mapString(m, "series_name", "name"), id),
		Cover:        normalizeCover(firstNonEmpty(mapString(card, "series_cover", "cover"), mapString(m, "series_cover"))),
		EpisodeCount: mapString(card, "episode_cnt"),
		Status:       catalogStatus(card),
		Heat:         catalogHeat(card),
		PlayCount:    mapString(card, "series_play_cnt", "play_cnt"),
		OnlineDate:   catalogOnlineDate(card, time.Now()),
		Vertical:     mapBool(card, "vertical"),
		VIP:          catalogVIP(card),
		Tags:         catalogTags(card),
	}
	if len(item.Cover) > 8192 { // guoapp hongguoCoverAddress：超长封面丢弃
		item.Cover = ""
	}
	item.Category = firstNonEmpty(mapString(card, "category_name", "categoryName", "category"))
	if item.Category == "" && len(item.Tags) > 0 {
		item.Category = item.Tags[0]
	}
	return item, true
}

// catalogStatus 完结状态：series_status 1/0 优先，否则按角标文本推断（guoapp releaseStatusFromRemark）。
func catalogStatus(card map[string]any) string {
	switch mapString(card, "series_status") {
	case "1":
		return "完结"
	case "0":
		return "连载"
	}
	remark := firstNonEmpty(mapString(card, "episode_right_text"), mapString(card, "sub_title"))
	switch {
	case strings.Contains(remark, "未完结"), strings.Contains(remark, "更新至"), strings.Contains(remark, "连载"):
		return "连载"
	case regexp.MustCompile(`全\d+集|\d+集全|已完结|大结局`).MatchString(remark), strings.Contains(remark, "完结"):
		return "完结"
	}
	return ""
}

// catalogHeat 热度：hot_score_data.score（可 parseFloat 的非负数值）→ hot_score → hot_score_data.text。
func catalogHeat(card map[string]any) string {
	hot := nestedMap(card, "hot_score_data")
	if score := firstNonEmpty(mapString(hot, "score"), mapString(card, "hot_score")); score != "" {
		if value, err := strconv.ParseFloat(score, 64); err == nil && value >= 0 && !math.IsInf(value, 0) && !math.IsNaN(value) {
			return score
		}
	}
	return mapString(hot, "text")
}

// catalogOnlineDate 上线日期：first_visible_time（秒/毫秒自适应，东八区，2000-2100）
// 优先；否则扫 sub_title_list 的「今日/昨日/X日上新」。
func catalogOnlineDate(card map[string]any, now time.Time) string {
	if ts := mapString(card, "first_visible_time"); ts != "" {
		if v, err := strconv.ParseInt(ts, 10, 64); err == nil {
			if v > 1_000_000_000_000 {
				v /= 1000
			}
			year := time.Unix(v, 0).In(chinaTimeZone).Year()
			if year >= 2000 && year <= 2100 {
				return time.Unix(v, 0).In(chinaTimeZone).Format("2006-01-02")
			}
		}
	}
	day := now.In(chinaTimeZone)
	for _, value := range anyList(card["sub_title_list"]) {
		label := mapString(asMap(value), "content")
		switch label {
		case "今日上新":
			return day.Format("2006-01-02")
		case "昨日上新":
			return day.AddDate(0, 0, -1).Format("2006-01-02")
		default:
			if strings.HasSuffix(label, "上新") {
				if date, err := time.ParseInLocation("2006-01-02", strings.TrimSuffix(label, "上新"), chinaTimeZone); err == nil {
					return date.Format("2006-01-02")
				}
			}
		}
	}
	return ""
}

// catalogVIP VIP 角标：landpage 卡片字段未在抓包中证实（guoapp-reference §7.3），
// 按 pay_info 出现付费标记启发式判定，命中才置位。
func catalogVIP(card map[string]any) bool {
	pay := nestedMap(card, "pay_info")
	if len(pay) == 0 {
		return false
	}
	for _, key := range []string{"pay_status", "need_pay", "is_vip", "vip", "pay_type"} {
		switch value := pay[key].(type) {
		case string:
			if value == "1" || value == "true" {
				return true
			}
		case bool:
			if value {
				return true
			}
		case json.Number:
			if n, err := value.Int64(); err == nil && n != 0 {
				return true
			}
		}
	}
	return false
}

// catalogTags 标签：tags[] + category_list[].name + category_schema（JSON 字符串）合并去重。
func catalogTags(card map[string]any) []string {
	var tags []string
	seen := map[string]bool{}
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			tags = append(tags, s)
		}
	}
	for _, t := range anyList(card["tags"]) {
		if s, ok := t.(string); ok {
			add(s)
		}
	}
	for _, row := range anyList(card["category_list"]) {
		add(mapString(asMap(row), "name"))
	}
	var categories []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal([]byte(mapString(card, "category_schema")), &categories) == nil {
		for _, item := range categories {
			add(item.Name)
		}
	}
	return tags
}

func mapBool(m map[string]any, key string) bool {
	switch value := m[key].(type) {
	case bool:
		return value
	case string:
		return value == "1" || value == "true"
	case json.Number:
		n, err := value.Int64()
		return err == nil && n != 0
	}
	return false
}

// ---- genre=all：三 genre 轮询合并（guoapp fetchHongguoAppCatalogCategory(ctx, "")） ----

// catalogCursorPrefix 组合游标前缀，与上游 session_id（日志 id 形态）区分。
const catalogCursorPrefix = "m."

// catalogCursorState 单 genre feed 的游标状态。
type catalogCursorState struct {
	Offset    int    `json:"o"`
	SessionID string `json:"s,omitempty"`
	Exhausted bool   `json:"e,omitempty"`
}

func encodeCatalogCursor(states map[string]catalogCursorState) string {
	content, err := json.Marshal(states)
	if err != nil {
		return ""
	}
	return catalogCursorPrefix + base64.RawURLEncoding.EncodeToString(content)
}

func decodeCatalogCursor(s string) map[string]catalogCursorState {
	states := map[string]catalogCursorState{}
	if !strings.HasPrefix(s, catalogCursorPrefix) {
		return nil
	}
	content, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(s, catalogCursorPrefix))
	if err != nil || json.Unmarshal(content, &states) != nil {
		return nil
	}
	return states
}

// catalogAllPage genre=all：对每个未翻完的 genre 各取一页合并（≤54 条），游标打包进 SessionID。
// 单 genre 失败时按 guoapp 口径将其标记翻完、其余 genre 继续；全部失败才报错。
func (c *Client) catalogAllPage(ctx context.Context, q CatalogQuery) (*CatalogPage, error) {
	states := decodeCatalogCursor(q.SessionID)
	if states == nil {
		states = map[string]catalogCursorState{}
	}
	var items []CatalogItem
	var failures []error
	hasMore := false
	total := 0
	seen := map[string]bool{}
	for _, genre := range catalogGenres {
		state := states[genre.Key]
		if state.Exhausted {
			continue
		}
		page, err := c.catalogGenrePage(ctx, genre.Key, state.Offset, state.SessionID)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			failures = append(failures, fmt.Errorf("%s: %w", genre.Name, err))
			state.Exhausted = true // 失败 feed 停止轮询，保留已取内容
			states[genre.Key] = state
			continue
		}
		for _, item := range page.Items {
			if seen[item.SeriesID] {
				continue
			}
			seen[item.SeriesID] = true
			items = append(items, item)
		}
		states[genre.Key] = catalogCursorState{Offset: page.Offset, SessionID: page.SessionID, Exhausted: !page.HasMore}
	}
	for _, state := range states {
		total += state.Offset
		if !state.Exhausted {
			hasMore = true
		}
	}
	if len(items) == 0 && len(failures) > 0 {
		return nil, errors.Join(failures...)
	}
	return &CatalogPage{
		Items:     items,
		Offset:    total,
		SessionID: encodeCatalogCursor(states),
		HasMore:   hasMore,
	}, nil
}

// ---- 筛选面板枚举（/api/v1/catalog/filters） ----

// CatalogFilterOption 筛选项；ID 供前端匹配（题材=标签名/面板 item id，状态/时段=约定枚举）。
type CatalogFilterOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// CatalogFilters 浏览页筛选面板三行枚举（题材/连载状态/上新时段）。
type CatalogFilters struct {
	Themes      []CatalogFilterOption `json:"themes"`
	Statuses    []CatalogFilterOption `json:"statuses"`
	OnlineTimes []CatalogFilterOption `json:"online_times"`
	Source      string                `json:"source"` // selector_panel | fallback
}

// fallbackFilters 兜底枚举：面板响应从未被 guoapp 消费、也未在抓包中捕获
// （guoapp-reference §2.1/§7.2）。题材取 #6 已拍板 mockup 的示例值；
// 状态/时段是客户端语义（按卡片 status/online_date 过滤），枚举稳定。
func fallbackFilters() *CatalogFilters {
	themes := []CatalogFilterOption{}
	for _, name := range []string{"都市", "甜宠", "逆袭", "重生", "穿越", "复仇", "战神", "古装"} {
		themes = append(themes, CatalogFilterOption{ID: name, Name: name})
	}
	return &CatalogFilters{
		Themes: themes,
		Statuses: []CatalogFilterOption{
			{ID: "", Name: "全部"}, {ID: "ongoing", Name: "连载中"}, {ID: "finished", Name: "已完结"},
		},
		OnlineTimes: []CatalogFilterOption{
			{ID: "", Name: "不限"}, {ID: "today", Name: "今日上新"}, {ID: "week", Name: "本周"}, {ID: "month", Name: "本月"},
		},
		Source: "fallback",
	}
}

// CatalogFilters 拉取筛选面板枚举：need_selector_panel=true 探测一次（进程内缓存 10 分钟），
// 拿不到可用面板时返回兜底枚举（Source=fallback），不报错。
func (c *Client) CatalogFilters(ctx context.Context) (*CatalogFilters, error) {
	c.filtersMu.Lock()
	cached := c.filters
	c.filtersMu.Unlock()
	if cached != nil && time.Now().Before(c.filtersExpires) {
		return cached, nil
	}
	payload := catalogLandpagePayload(CatalogGenreShortPlay, 0, "")
	payload["need_selector_panel"] = true
	result, err := c.appRequest(ctx, "POST", "/reading/distribution/category/landpage/v/", nil, payload, false)
	var filters *CatalogFilters
	if err == nil {
		filters = filtersFromPanel(result)
	}
	if filters == nil {
		filters = fallbackFilters()
	}
	c.filtersMu.Lock()
	c.filters, c.filtersExpires = filters, time.Now().Add(10*time.Minute)
	c.filtersMu.Unlock()
	return filters, nil
}

// filtersFromPanel 从面板响应提取枚举。landpage 面板结构未抓包证实，此处按
// 同族筛选 schema（榜单 cell_selector.panel_selector，guoapp-reference §5.5/§2.1）
// 宽容解析：data 下找 selector_panel/panel_selector/cell_selector，行分组带
// row_key/row_name，选项 selector_item_id/show_name。行与三个维度的对应按
// row_key（category_dim_theme/creation_status/online_time）或 row_name 关键词匹配。
func filtersFromPanel(result map[string]any) *CatalogFilters {
	data := nestedMap(result, "data")
	for _, key := range []string{"selector_panel", "panel_selector", "cell_selector"} {
		panel, ok := data[key].(map[string]any)
		if !ok {
			continue
		}
		filters := &CatalogFilters{Source: "selector_panel"}
		for _, row := range panelRows(panel) {
			options := panelRowOptions(row)
			if len(options) == 0 {
				continue
			}
			rowKey := mapString(row, "row_key", "key", "type")
			rowName := mapString(row, "row_name", "name")
			switch {
			case strings.Contains(rowKey, "theme") || strings.Contains(rowName, "题材") || strings.Contains(rowName, "主题"):
				filters.Themes = options
			case strings.Contains(rowKey, "creation_status") || strings.Contains(rowName, "连载") || strings.Contains(rowName, "状态"):
				filters.Statuses = options
			case strings.Contains(rowKey, "online_time") || strings.Contains(rowName, "上新") || strings.Contains(rowName, "时段"):
				filters.OnlineTimes = options
			}
		}
		if len(filters.Themes)+len(filters.Statuses)+len(filters.OnlineTimes) > 0 {
			return filters
		}
	}
	return nil
}

// panelRows 行分组可能在 rows/inner_rows/list 或 panel 本身即行数组。
func panelRows(panel map[string]any) []map[string]any {
	for _, key := range []string{"rows", "inner_rows", "list", "items"} {
		if rows := mapList(panel[key]); len(rows) > 0 {
			return rows
		}
	}
	if rows := mapList(panel); rows != nil {
		return rows
	}
	return nil
}

func panelRowOptions(row map[string]any) []CatalogFilterOption {
	for _, key := range []string{"options", "selector_items", "items", "sub_rows"} {
		list := mapList(row[key])
		if len(list) == 0 {
			continue
		}
		var options []CatalogFilterOption
		for _, item := range list {
			name := mapString(item, "show_name", "name", "title")
			if name == "" {
				continue
			}
			options = append(options, CatalogFilterOption{
				ID:   firstNonEmpty(mapString(item, "selector_item_id", "id", "item_id"), name),
				Name: name,
			})
		}
		if len(options) > 0 {
			return options
		}
	}
	return nil
}

func mapList(v any) []map[string]any {
	rows, _ := v.([]any)
	if len(rows) == 0 {
		return nil
	}
	list := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if m, ok := row.(map[string]any); ok {
			list = append(list, m)
		}
	}
	return list
}
