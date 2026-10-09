package hongguo

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/2017fighting/guo/internal/ass"
	"github.com/2017fighting/guo/internal/pipeline"
)

const (
	danmakuWindowMS     = 30_000
	danmakuMaxDuration  = 24 * 60 * 60 * 1000
	danmakuCountPerPage = 90
)

// danmakuPage 一页（一个 30s 窗口）的解析结果。
type danmakuPage struct {
	items  []ass.Comment
	nextMS int64
}

// DanmakuAll 实现 pipeline.Source：按 30 秒窗口游标拉完整集弹幕。
func (c *Client) DanmakuAll(ctx context.Context, seriesID, vid string, durationMS int64) ([]ass.Comment, error) {
	if !numericID.MatchString(seriesID) || !numericID.MatchString(vid) ||
		durationMS <= 0 || durationMS > danmakuMaxDuration {
		return nil, errors.New("弹幕请求参数无效")
	}
	seen := map[string]bool{}
	var all []ass.Comment
	start := int64(0)
	for start < durationMS {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		page, err := c.danmakuWindow(ctx, seriesID, vid, start, durationMS)
		if err != nil {
			return nil, err
		}
		for _, item := range page.items {
			if !seen[item.ID] {
				seen[item.ID] = true
				all = append(all, item)
			}
		}
		if page.nextMS <= start {
			return nil, errors.New("红果弹幕时间段游标未前进")
		}
		start = page.nextMS
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].TimeMS < all[j].TimeMS })
	return all, nil
}

// danmakuWindow 拉取单个 30s 窗口（弹幕签名全套在此生效）。
func (c *Client) danmakuWindow(ctx context.Context, seriesID, vid string, start, duration int64) (danmakuPage, error) {
	body := map[string]any{
		"comment_source": 601, "server_channel": 1000, "group_id": vid, "group_type": 30,
		"comment_type": 20, "sort": 1, "count": danmakuCountPerPage, "cursor": "", "aid": 8662, "compliance_status": 0,
		"business_param": map[string]any{
			"book_id": seriesID, "start_offset_time": start, "playlet_item_duration": duration,
			"need_danmaku_guide_type": []int{1, 3, 4, 2},
		},
	}
	result, err := c.appRequest(ctx, http.MethodPost, "/novel/commentapi/comment/list/"+vid+"/v1/", nil, body, true)
	if err != nil {
		return danmakuPage{}, err
	}
	return parseDanmakuPage(result, vid, start, duration)
}

// parseDanmakuPage 解析一页弹幕（口径对齐 guoapp parseHongguoDanmaku）。
func parseDanmakuPage(result map[string]any, videoID string, start, duration int64) (danmakuPage, error) {
	data := nestedMap(result, "data")
	rows, valid := data["data_list"].([]any)
	next, err := strconv.ParseInt(mapString(nestedMap(data, "extra"), "next_query_danmaku_list_time"), 10, 64)
	if !valid || err != nil || next <= start || next > danmakuMaxDuration {
		return danmakuPage{}, errors.New("红果弹幕时间段格式无效")
	}
	page := danmakuPage{items: []ass.Comment{}, nextMS: min(next, duration)}
	seen := map[string]bool{}
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		comment := nestedMap(row, "comment")
		common := nestedMap(comment, "common")
		if mapString(common, "group_id") != videoID || mapString(common, "status") != "1" {
			continue
		}
		position, perr := strconv.ParseInt(mapString(nestedMap(comment, "expand"), "offset_time"), 10, 64)
		text := strings.TrimSpace(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
				return ' '
			}
			return r
		}, mapString(nestedMap(common, "content"), "text")))
		id := mapString(comment, "comment_id")
		if perr != nil || position < start || position >= page.nextMS || text == "" || id == "" || len(id) > 120 || seen[id] {
			continue
		}
		if runes := []rune(text); len(runes) > 180 {
			text = string(runes[:180]) + "…"
		}
		seen[id] = true
		page.items = append(page.items, ass.Comment{ID: id, Text: text, TimeMS: position})
		if len(page.items) == danmakuCountPerPage {
			break
		}
	}
	sort.SliceStable(page.items, func(i, j int) bool { return page.items[i].TimeMS < page.items[j].TimeMS })
	return page, nil
}

// 编译期接口检查：*Client 实现 pipeline.Source。
var _ pipeline.Source = (*Client)(nil)
