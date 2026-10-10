package rankings

// api3 请求构造 + 轻量签名 + 响应解析。参数口径全部来自真机抓包
// （docs/research/raw/flows，guoapp-reference §5.2），字段值与抓包逐项对齐。

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/bits"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	planPath       = "/reading/bookapi/plan/v:version/"
	cellChangePath = "/reading/bookapi/bookmall/cell/change/v:version/"

	// defaultCellID 抓包固定 cell_id（plan 响应 cell_view.cell_id 同值）。
	defaultCellID = "7470092475068071998"

	maxBodyBytes = 20 << 20
)

// api3Identity 抓包设备参数尾串中的会话身份（进程内固定，与 device 参数其余
// 常量一起构成完整尾串）。
type api3Identity struct {
	DeviceID           string
	InstallID          string
	SessionUUID        string
	NormalSessionID    string
	ColdStartSessionID string
	CDID               string
}

func newAPI3Identity() api3Identity {
	return api3Identity{
		DeviceID:           randomDeviceID(),
		InstallID:          randomDeviceID(),
		SessionUUID:        randomUUID(),
		NormalSessionID:    randomUUID() + "#1",
		ColdStartSessionID: randomUUID(),
		CDID:               randomUUID(),
	}
}

// randomDeviceID 19 位十进制设备号（与 hongguo.newDeviceID 同口径）。
func randomDeviceID() string {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return strconv.FormatUint(1_000_000_000_000_000_000+binary.BigEndian.Uint64(random[:])%8_000_000_000_000_000_000, 10)
}

// randomUUID v4 形态（仅作会话标识，不要求密码学严格版本位）。
func randomUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	hexed := hex.EncodeToString(b[:])
	return hexed[0:8] + "-" + hexed[8:12] + "-" + hexed[12:16] + "-" + hexed[16:20] + "-" + hexed[20:32]
}

// planParams plan 入口请求参数（scene 部分 + 设备尾串，抓包 plan 请求逐项对齐）。
func planParams(id api3Identity) map[string]string {
	params := map[string]string{
		"scene": "10", "from": "video_ranklist", "new_search_middle_page": "false",
		"bookstore_tab_type": "26", "query_history_removed": "false", "user_is_login": "0",
		"bookstore_tab": "0", "search_middle_page_version": "0", "pad_column_detail": "0",
		"hot_word_exchange": "false", "limit": "0", "offset": "0", "selected_items": "all",
		"session_uuid": id.SessionUUID, "total_chapter_num": "0", "current_chapter_num": "0",
		"last_consume_interval": "0", "last_search_page_interval": "0",
		"sub_selected_items": Boards[0].ID, "last_book_consume_time": "0",
		"panel_selected_items": "", "background_selected_items": "", "post_id": "0",
		"is_horizontal_screen": "false", "reader_single_col_from_ip_strategy": "false",
	}
	for key, value := range deviceParams(id) {
		params[key] = value
	}
	return params
}

// cellChangeParams 换榜/翻页请求参数（50 个场景参数 + 设备尾串）。首页换榜：
// offset=0、空游标、unlimited_selector_change_type=2；翻页：回传 offset/
// session_id/filter_ids/rank_version、type=1。
func cellChangeParams(cellID, boardID, tab string, offset int, cur Cursor, changeType string, id api3Identity) map[string]string {
	params := map[string]string{
		"app_launch_times": "0", "author_id": "0", "background_selected_items": "",
		"book_comment_id": "0", "category_id": "0", "celebrity_user_id": "",
		"cell_id": cellID, "cell_sub_id": "0", "client_req_type": "2", "client_template": "2",
		"cold_start_session": "0", "current_chapter_num": "0", "disable_digg_stat": "false",
		"ecom_category_id": "0", "ecom_impression_start_time": "0", "ecom_refresh_type": "0",
		"ecom_sort_by": "0", "genre": "0", "genre_type": "0", "idol_tag_id": "0",
		"inner_algo": "0", "inner_category_id": "0", "is_horizontal_screen": "false",
		"item_id": "0", "limit": "0", "offset": strconv.Itoa(offset), "pad_column_cover": "0",
		"pad_column_detail": "0", "page": "0", "page_entry_time": "0", "panel_selected_items": "",
		"plan_id": "0", "post_id": "0", "rank_sub_info_id": "0", "rank_version": cur.RankVersion,
		"reader_single_col_from_ip_strategy": "false", "related_book_id": "0",
		"screen_width_px": "2832", "search_tab_type": "0", "selected_items": tab,
		"session_uuid": id.SessionUUID, "source_tab_type": "0", "sub_genre": "0",
		"sub_selected_items": boardID, "sub_tag_id": "0", "support_gender_list": "false",
		"tab_type": "26", "tag_id": "0", "total_chapter_num": "0",
		"unlimited_selector_change_type": changeType, "video_tab_cold_start": "0",
		"web_page_version_code": "0",
	}
	if cur.SessionID != "" {
		params["session_id"] = cur.SessionID
	}
	if len(cur.FilterIDs) > 0 {
		params["filter_ids"] = strings.Join(cur.FilterIDs, ",")
	}
	for key, value := range deviceParams(id) {
		params[key] = value
	}
	return params
}

// deviceParams 设备参数尾串（抓包真机常量 + 会话身份 + 实时 _rticket）。
func deviceParams(id api3Identity) map[string]string {
	return map[string]string{
		"iid": id.InstallID, "device_id": id.DeviceID, "ac": "wifi", "channel": "gray_test_64",
		"aid": "8662", "app_name": "novelread", "version_code": "73970", "version_name": "7.3.9.70",
		"device_platform": "android", "os": "android", "ssmix": "a",
		"device_type": "sdk_gphone64_arm64", "device_brand": "google", "language": "en",
		"os_api": "33", "os_version": "13", "manifest_version_code": "73970",
		"resolution": "1080*2209", "dpi": "420", "update_version_code": "73970",
		"_rticket":                  strconv.FormatInt(time.Now().UnixMilli(), 10),
		"normal_session_cnt_in_day": "16", "gender": "2", "cold_start_session_cnt_in_day": "8",
		"host_abi": "arm64-v8a", "dragon_device_type": "phone", "sys_mini_window": "1",
		"pv_player": "73970", "app_mini_window": "0", "normal_session_id": id.NormalSessionID,
		"compliance_status": "0", "har_status": "0", "cold_start_session_id": id.ColdStartSessionID,
		"cold_start_session_cnt_in_life": "8", "charging": "0", "normal_session_cnt_in_life": "16",
		"is_power_save_mode": "0", "app_dark_mode": "0", "screen_brightness": "102",
		"battery_pct": "100", "down_speed": "30000", "sys_dark_mode": "0",
		"need_personal_recommend": "1", "player_so_load": "1", "font_scale": "100",
		"is_android_pad_screen": "0", "network_type": "4",
		"rom_version":    "sdk_gphone64_arm64-userdebug 13 TE1A.240213.009 12342917 dev-keys",
		"current_volume": "33", "cdid": id.CDID,
		// recommend_extra = base64({"recent_dislike_gid":[],"session_app_stay_time":0}\n)
		"recommend_extra": "eyJyZWNlbnRfZGlzbGlrZV9naWQiOltdLCJzZXNzaW9uX2FwcF9zdGF5X3RpbWUiOjB9\n",
	}
}

// encodeQuery 字典序编码（与 hongguo appRequest 的 query.Encode 一致；
// Gorgon 对编码后的 RawQuery 签名，顺序只需自洽）。
func encodeQuery(params map[string]string) string {
	values := make([]string, 0, len(params))
	for key, value := range params {
		values = append(values, urlEscape(key)+"="+urlEscape(value))
	}
	// 简单字典序（键值均为 ASCII + 少量中文/特殊值走转义后比较）
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
	return strings.Join(values, "&")
}

func urlEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		default:
			b.WriteString(fmt.Sprintf("%%%02X", c))
		}
	}
	return b.String()
}

// refreshTicket 每次尝试刷新 _rticket（签名随 RawQuery 变化，须在构造请求时进行）。
func refreshTicket(params map[string]string) map[string]string {
	refreshed := make(map[string]string, len(params))
	for key, value := range params {
		refreshed[key] = value
	}
	refreshed["_rticket"] = strconv.FormatInt(time.Now().UnixMilli(), 10)
	return refreshed
}

// signRankings 轻量 Gorgon（0x8404：query 摘要 + 时间戳；GET 无 body），
// 与 hongguo.signRequest 同构（榜单通道实测可直接复用该口径，见包注释）。
// 不带 X-Argus/X-Ladon/X-Helios/X-Medusa。
func signRankings(request *http.Request, body []byte, now time.Time) {
	timestamp := uint32(now.Unix())
	queryHash := md5.Sum([]byte(request.URL.RawQuery))
	var payload [20]byte
	copy(payload[:4], queryHash[:4])
	if body != nil {
		bodyHash := md5.Sum(body)
		copy(payload[4:8], bodyHash[:4])
		request.Header.Set("X-SS-STUB", strings.ToUpper(hex.EncodeToString(bodyHash[:])))
	}
	copy(payload[12:16], []byte{0, 6, 11, 28})
	binary.BigEndian.PutUint32(payload[16:], timestamp)
	key := [...]byte{0x44, 0xb9, 0xb9, 0xd9, 0xa4, 0xae, 0xf9, 0xfc, 0xa4, 0x93, 0xaa, 0x75, 0x7c, 0xa3, 0xc2, 0xc4, 0xa4, 0x96, 0x93, 0x8f}
	for index := range payload {
		payload[index] ^= key[index]
	}
	for index := range payload {
		mixed := bits.RotateLeft8(payload[index], 4) ^ payload[(index+1)%len(payload)]
		payload[index] = bits.Reverse8(mixed) ^ 0xff ^ byte(len(payload))
	}
	signature := append([]byte{0x84, 0x04, 0x40, 0x1c, 0, 0}, payload[:]...)
	request.Header.Set("X-Khronos", strconv.FormatUint(uint64(timestamp), 10))
	request.Header.Set("X-Gorgon", hex.EncodeToString(signature))
	request.Header.Set("X-SS-Req-Ticket", strconv.FormatInt(now.UnixMilli(), 10))
}

// ---- 响应解析 ----

// parseBoardResponse 解析 cell/change（及 plan 自带首页）响应。
// 兼容两种形态（真机实测）：
//   - map 形态（抓包 bodies/*.json）：data.cell_view.{cell_data,next_offset,...}
//   - list 形态（真机 plan）：data[0].{cell_data,next_offset,...}
//
// 分页防御同目录 feed：next_offset 必须严格前进、session_id ≤4096 且无控制字符。
func parseBoardResponse(content []byte, offset int) (*BoardPage, error) {
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil || result == nil {
		return nil, errors.New("红果榜单返回格式异常")
	}
	element := asMap(result["data"])
	if element == nil {
		if list := anyList(result["data"]); len(list) > 0 {
			element = asMap(list[0])
		}
	}
	if element == nil {
		return nil, errors.New("红果榜单返回缺少数据")
	}
	rows := elementRows(element, "cell_data")
	if rows == nil {
		rows = elementRows(asMap(element["cell_view"]), "cell_data")
	}
	items := make([]Item, 0, len(rows))
	ids := make([]string, 0, len(rows))
	for index, row := range rows {
		item, ok := itemFromVideoData(row, offset+index+1)
		if !ok {
			continue
		}
		items = append(items, item)
		ids = append(ids, item.SeriesID)
	}
	if len(rows) > 0 && len(items) == 0 {
		return nil, errors.New("榜单未返回可识别的剧集")
	}
	if len(rows) == 0 {
		return nil, errors.New("榜单暂无剧集数据")
	}
	next, nextErr := strconv.Atoi(firstNonEmpty(mapGet(element, "next_offset"), mapGet(asMap(element["cell_view"]), "next_offset")))
	hasMore, hasMoreOK := elementBool(element, "has_more")
	if !hasMoreOK {
		if cellView := asMap(element["cell_view"]); cellView != nil {
			hasMore, hasMoreOK = elementBool(cellView, "has_more")
		}
	}
	if !hasMoreOK {
		return nil, errors.New("榜单分页标记无效，请稍后重试")
	}
	if nextErr != nil {
		if !hasMore {
			next = offset
		} else {
			return nil, errors.New("榜单分页游标无效，请稍后重试")
		}
	}
	if hasMore && (next <= offset || next > 1_000_000) {
		return nil, errors.New("榜单分页未前进，请稍后重试")
	}
	if !hasMore && next < offset {
		next = offset
	}
	session := firstNonEmpty(mapGet(element, "session_id"), mapGet(asMap(element["cell_view"]), "session_id"))
	if len(session) > 4096 || strings.ContainsAny(session, "\r\n\x00") {
		return nil, errors.New("榜单分页会话无效，请稍后重试")
	}
	return &BoardPage{
		Items:       items,
		Offset:      next,
		SessionID:   session,
		FilterIDs:   ids,
		RankVersion: firstNonEmpty(mapGet(element, "rank_version"), mapGet(asMap(element["cell_view"]), "rank_version")),
		HasMore:     hasMore,
	}, nil
}

// parsePlanResponse 解析 plan 响应：cell_id + 榜单分类（外层 tab → 榜单清单）。
func parsePlanResponse(content []byte) (*planResult, error) {
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil || result == nil {
		return nil, errors.New("红果榜单入口返回格式异常")
	}
	element := asMap(result["data"])
	if element == nil {
		if list := anyList(result["data"]); len(list) > 0 {
			element = asMap(list[0])
		}
	}
	if element == nil {
		return nil, errors.New("红果榜单入口返回缺少数据")
	}
	cellID := firstNonEmpty(mapGet(element, "cell_id_str"), mapGet(element, "cell_id"))
	if cellID == "" {
		if view := asMap(element["cell_view"]); view != nil {
			cellID = firstNonEmpty(mapGet(view, "cell_id_str"), mapGet(view, "cell_id"))
		}
	}
	if cellID == "" {
		return nil, errors.New("榜单入口未给出 cell_id")
	}
	selector := asMap(element["cell_selector"])
	if selector == nil {
		if view := asMap(element["cell_view"]); view != nil {
			selector = asMap(view["cell_selector"])
		}
	}
	boards := taxonomyFromSelector(selector)
	return &planResult{CellID: cellID, Boards: boards}, nil
}

// taxonomyFromSelector plan cell_selector：outer_row.items[] 为外层 tab，
// 各 tab 的 sub_cell_selector.outer_row.items[] 为该族榜单清单。
func taxonomyFromSelector(selector map[string]any) []BoardInfo {
	var boards []BoardInfo
	outer := asMap(selector["outer_row"])
	for _, tabRow := range anyList(outer["items"]) {
		tab := asMap(tabRow)
		tabID := mapGet(tab, "selector_item_id")
		if tabID == "" {
			continue
		}
		sub := asMap(tab["sub_cell_selector"])
		subOuter := asMap(sub["outer_row"])
		for _, boardRow := range anyList(subOuter["items"]) {
			board := asMap(boardRow)
			id := mapGet(board, "selector_item_id")
			if id == "" {
				continue
			}
			boards = append(boards, BoardInfo{ID: id, Name: mapGet(board, "show_name"), Tab: tabID})
		}
	}
	return boards
}

// elementRows 兼容取 cell_data 列表。
func elementRows(element map[string]any, key string) []any {
	if element == nil {
		return nil
	}
	return anyList(element[key])
}

var numericSeriesID = regexp.MustCompile(`^[0-9]{1,32}$`)

// itemFromVideoData 单条 cell（cell_data 行）→ Item。条目在 cell.video_data[]
// （每 cell 1 条）；rank 取 recommend_info.rank（JSON 字符串，样本存在重复值），
// 缺失时回退条目序号（offset+序）。
func itemFromVideoData(row any, fallbackRank int) (Item, bool) {
	cell := asMap(row)
	if cell == nil {
		return Item{}, false
	}
	card := cell
	if nested := asMap(cell["video_data"]); nested != nil {
		card = nested
	} else if list := anyList(cell["video_data"]); len(list) > 0 {
		card = asMap(list[0])
	}
	if card == nil {
		return Item{}, false
	}
	id := mapGet(card, "series_id")
	if !numericSeriesID.MatchString(id) {
		return Item{}, false
	}
	rank := 0
	if info := mapGet(card, "recommend_info"); info != "" {
		var parsed struct {
			Rank json.Number `json:"rank"`
		}
		if json.Unmarshal([]byte(info), &parsed) == nil {
			if n, err := parsed.Rank.Int64(); err == nil {
				rank = int(n)
			}
		}
	}
	if rank <= 0 {
		rank = fallbackRank
	}
	item := Item{
		SeriesID:     id,
		Title:        firstNonEmpty(mapGet(card, "title"), mapGet(card, "series_title"), id),
		Cover:        normalizeCover(mapGet(card, "cover")),
		Desc:         mapGet(card, "video_desc"),
		Vertical:     elementBoolValue(card, "vertical"),
		Rank:         rank,
		Score:        mapGet(card, "score"),
		PlayCount:    mapGet(card, "play_cnt"),
		EpisodeCount: mapGet(card, "episode_cnt"),
		Heat:         heatText(card),
	}
	if len(item.Cover) > 8192 {
		item.Cover = ""
	}
	return item, true
}

// heatText secondary_info_list 热度文本：优先含「热度」的条目，
// 否则第一条非空（口碑榜为「1.6万人评分」/「71.9万收藏」形态）。
func heatText(card map[string]any) string {
	var first string
	for _, row := range anyList(card["secondary_info_list"]) {
		content := mapGet(asMap(row), "content")
		if content == "" {
			continue
		}
		if first == "" {
			first = content
		}
		if strings.Contains(content, "热度") {
			return content
		}
	}
	return first
}

// normalizeCover 封面规范化：协议补全 + reading-sign 签名 HEIC 重写。
// pN-reading-sign.fqnovelpic.com 封面带签名且为 HEIC，浏览器无法渲染；
// 同图 pN-novel.byteimg.com 渠道可内容协商出 jpeg/webp，故重写为
// <hash>~tplv-shrink:640:0.image（去查询串）。与 hongguo 包 normalizeCover
// 同口径（rankings 包独立，内联实现）。
var signedHeicCover = regexp.MustCompile(`^https?://p(\d+)-reading-sign\.fqnovelpic\.com/novel-pic/([^~/?#]+)~`)

func normalizeCover(cover string) string {
	if strings.HasPrefix(cover, "//") {
		cover = "https:" + cover
	}
	if m := signedHeicCover.FindStringSubmatch(cover); m != nil {
		return fmt.Sprintf("https://p%s-novel.byteimg.com/novel-pic/%s~tplv-shrink:640:0.image", m[1], m[2])
	}
	return cover
}

// ---- 宽松取值助手（与 hongguo 包同口径，包内自持） ----

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func anyList(v any) []any {
	list, _ := v.([]any)
	return list
}

func mapGet(m map[string]any, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, key := range keys {
		switch value := m[key].(type) {
		case string:
			if value != "" {
				return value
			}
		case json.Number:
			return value.String()
		case bool:
			return strconv.FormatBool(value)
		}
	}
	return ""
}

func elementBool(m map[string]any, key string) (bool, bool) {
	if m == nil {
		return false, false
	}
	switch value := m[key].(type) {
	case bool:
		return value, true
	case json.Number:
		if n, err := value.Int64(); err == nil {
			return n != 0, true
		}
	case string:
		return value == "1" || value == "true", true
	}
	return false, false
}

func elementBoolValue(m map[string]any, key string) bool {
	value, _ := elementBool(m, key)
	return value
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
