package hongguo

// Web（官网 SSR）线路：详情兜底 + 播放器页取流。
// 依据 docs/research/hongguo-protocol.md §3.3/§4.2 与 guoapp
// fetchHongguoWebChapters / resolveHongguoWebMedia 的解析口径：
//   - 页面内嵌 window._ROUTER_DATA JSON；loader 键 detail_page…/player_page…
//   - 详情：seriesDetail.vid_list = 纯 vid 数组（下标+1 即集号）
//   - 播放器：校验 vid/series_id 一致，video_player_info 地址族（明文 h264，无 CENC）
//   - 请求带 iPhone UA + Referer hongguoduanju.com/

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/2017fighting/guo/internal/pipeline"
)

var routerDataRe = regexp.MustCompile(`(?s)(?:window\.)?_ROUTER_DATA\s*=\s*`)

const webUA = pipeline.IPhoneUA

// fetchWeb 拉官网页面文本。
func (c *Client) fetchWeb(ctx context.Context, path string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, webBaseURL+path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", webUA)
	req.Header.Set("Referer", webBaseURL+"/")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("红果官网 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// parseRouterData 提取页面内嵌 _ROUTER_DATA JSON。
func parseRouterData(raw string) map[string]any {
	idx := routerDataRe.FindStringIndex(raw)
	if idx == nil {
		return nil
	}
	var data map[string]any
	dec := json.NewDecoder(strings.NewReader(raw[idx[1]:]))
	dec.UseNumber()
	if err := dec.Decode(&data); err != nil {
		return nil
	}
	return data
}

// routerLoaderMap 按（前缀）名取 loaderData 页面对象。
func routerLoaderMap(data map[string]any, names ...string) map[string]any {
	loader, _ := data["loaderData"].(map[string]any)
	for _, name := range names {
		if page, _ := loader[name].(map[string]any); len(page) > 0 {
			return page
		}
	}
	for key, value := range loader {
		for _, name := range names {
			if strings.TrimSuffix(name, "$") != "" && strings.HasPrefix(key, strings.TrimSuffix(name, "$")) {
				if page, _ := value.(map[string]any); len(page) > 0 {
					return page
				}
			}
		}
	}
	return map[string]any{}
}

// webDetail 官网详情兜底：seriesDetail.vid_list → 分集。
func (c *Client) webDetail(ctx context.Context, seriesID string) (*pipeline.DramaMeta, error) {
	body, err := c.fetchWeb(ctx, "/detail?series_id="+url.QueryEscape(seriesID))
	if err != nil {
		return nil, err
	}
	page := routerLoaderMap(parseRouterData(body), "detail_page", "detail_")
	detail, _ := page["seriesDetail"].(map[string]any)
	if len(detail) == 0 {
		return nil, errors.New("红果官网详情为空")
	}
	meta := dramaFromCard(detail)
	meta.SeriesID = seriesID
	meta.Title = firstNonEmpty(mapString(detail, "series_name", "series_title", "name"), seriesID)
	if meta.CoverURL == "" {
		meta.CoverURL = normalizeCover(mapString(detail, "cover", "cover_url"))
	}
	for i, v := range anyList(detail["vid_list"]) {
		vid := strings.TrimSpace(fmt.Sprint(v))
		if vid == "" || vid == "<nil>" || !numericID.MatchString(vid) {
			continue
		}
		meta.Episodes = append(meta.Episodes, pipeline.EpisodeInfo{Index: i + 1, VID: vid})
	}
	if len(meta.Episodes) == 0 {
		return nil, errors.New("红果官网详情没有分集")
	}
	return &meta, nil
}

// webStream 官网播放器页取流：标准 h264 MP4 直链，无 CENC 加密。
func (c *Client) webStream(ctx context.Context, seriesID, vid string) (*pipeline.Stream, error) {
	body, err := c.fetchWeb(ctx, "/player/"+url.PathEscape(seriesID)+"/"+url.PathEscape(vid))
	if err != nil {
		return nil, err
	}
	page := routerLoaderMap(parseRouterData(body), "player_", "player_page")
	if mapString(page, "vid") != vid || mapString(page, "series_id") != seriesID {
		return nil, errors.New("红果网页未返回该集（可能仅允许试看）")
	}
	info, _ := page["video_player_info"].(map[string]any)
	addresses := mediaAddresses(info)
	if len(addresses) == 0 {
		return nil, errors.New("红果网页未提供公开播放地址")
	}
	durationSec, _ := strconv.ParseFloat(mapString(info, "duration"), 64)
	return &pipeline.Stream{
		URL:        addresses[0],
		Referer:    webBaseURL + "/",
		DurationMS: int64(durationSec * 1000),
	}, nil
}
