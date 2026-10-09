package hongguo

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/2017fighting/guo/internal/pipeline"
)

// Detail 实现 pipeline.Source：App 详情接口（video_detail）。
func (c *Client) Detail(ctx context.Context, seriesID string) (*pipeline.DramaMeta, error) {
	if !numericID.MatchString(seriesID) {
		return nil, errors.New("红果剧集 ID 无效")
	}
	result, err := c.appRequest(ctx, http.MethodPost, "/novel/player/video_detail/v1/", nil,
		map[string]any{"series_id": seriesID}, false)
	if err != nil {
		return nil, err
	}
	detail := nestedMap(result, "data", "video_data")
	if mapString(detail, "series_id_str", "series_id") != seriesID {
		return nil, errors.New("红果 App 未返回所请求的剧集")
	}
	meta := dramaFromCard(detail)
	meta.SeriesID = seriesID

	seen := map[string]bool{}
	episodes := map[int]bool{}
	for _, row := range anyList(detail["video_list"]) {
		video, _ := row.(map[string]any)
		videoID := mapString(video, "vid")
		index, err := strconv.Atoi(mapString(video, "vid_index"))
		if err != nil || index < 1 || !numericID.MatchString(videoID) {
			return nil, errors.New("红果 App 分集编号或视频 ID 无效")
		}
		if identifier := mapString(video, "series_id"); identifier != "" && identifier != seriesID {
			return nil, errors.New("红果 App 返回了其他剧集的分集")
		}
		if seen[videoID] || episodes[index] {
			return nil, errors.New("红果 App 返回了重复分集")
		}
		seen[videoID], episodes[index] = true, true
		meta.Episodes = append(meta.Episodes, pipeline.EpisodeInfo{Index: index, VID: videoID})
	}
	sort.Slice(meta.Episodes, func(i, j int) bool { return meta.Episodes[i].Index < meta.Episodes[j].Index })
	total, _ := strconv.Atoi(mapString(detail, "episode_cnt"))
	if len(meta.Episodes) == 0 || total > len(meta.Episodes) {
		return nil, errors.New("红果 App 未返回完整分集列表")
	}
	for i, ep := range meta.Episodes {
		if ep.Index != i+1 {
			return nil, errors.New("红果 App 分集列表不连续")
		}
	}
	return &meta, nil
}

// dramaFromCard 从 video_data 卡片提取剧级元数据（字段别名对齐 guoapp hongguoDramaFromAny）。
func dramaFromCard(m map[string]any) pipeline.DramaMeta {
	meta := pipeline.DramaMeta{
		Title:    firstNonEmpty(mapString(m, "series_title", "series_name", "title"), "未命名剧"),
		Plot:     mapString(m, "series_intro", "video_desc"),
		CoverURL: normalizeCover(mapString(m, "series_cover", "cover")),
	}
	// 年份：first_visible_time 时间戳（毫秒/秒自适应），东八区。
	if ts := mapString(m, "first_visible_time"); ts != "" {
		if v, err := strconv.ParseInt(ts, 10, 64); err == nil {
			if v > 1_000_000_000_000 { // 毫秒
				v /= 1000
			}
			meta.Year = time.Unix(v, 0).In(time.FixedZone("CST", 8*3600)).Format("2006")
		}
	}
	// 标签：tags + category_list[].name 合并去重。
	seen := map[string]bool{}
	addTag := func(s string) {
		s = firstNonEmpty(s)
		if s != "" && !seen[s] {
			seen[s] = true
			meta.Genres = append(meta.Genres, s)
		}
	}
	for _, t := range anyList(m["tags"]) {
		if s, ok := t.(string); ok {
			addTag(s)
		}
	}
	for _, row := range anyList(m["category_list"]) {
		if cat, ok := row.(map[string]any); ok {
			addTag(mapString(cat, "name"))
		}
	}
	if len(meta.Genres) == 0 {
		if cat := firstNonEmpty(mapString(m, "category_name", "categoryName", "category")); cat != "" {
			meta.Genres = []string{cat}
		}
	}
	return meta
}

func normalizeCover(u string) string {
	if u == "" {
		return ""
	}
	// 协议补全：//xxx → https://xxx
	if len(u) >= 2 && u[:2] == "//" {
		return "https:" + u
	}
	return u
}
