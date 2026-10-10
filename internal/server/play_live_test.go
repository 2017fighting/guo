package server

// 真机冒烟：GUO_LIVE=1 时才执行。播放链路：真源取流（自动链）→ 代理拉首段
// （Range 206）→ 弹幕首窗。GUO_LIVE_SERIES 可指定剧（默认取目录首位）。

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/2017fighting/guo/internal/hongguo"
)

func TestPlayLiveSmoke(t *testing.T) {
	if os.Getenv("GUO_LIVE") != "1" {
		t.Skip("GUO_LIVE=1 才跑真机冒烟")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	source := hongguo.NewClient()
	seriesID := os.Getenv("GUO_LIVE_SERIES")
	if seriesID == "" {
		page, err := source.CatalogPage(ctx, hongguo.CatalogQuery{Genre: hongguo.CatalogGenreShortPlay})
		if err != nil || len(page.Items) == 0 {
			t.Fatalf("真机目录失败（可用 GUO_LIVE_SERIES 指定剧）: %v items=%d", err, len(page.Items))
		}
		seriesID = page.Items[0].SeriesID
	}
	detail, err := source.Detail(ctx, seriesID)
	if err != nil || len(detail.Episodes) == 0 {
		t.Fatalf("真机详情失败: %v episodes=%d", err, len(detail.Episodes))
	}
	vid := detail.Episodes[0].VID

	srv := &Server{Streams: source, Danmaku: source}
	ts := httptestUnstarted(srv)
	defer ts.Close()

	// 1) 取流：形状 + 自动链生效线路
	resp, err := http.Get(ts.URL + fmt.Sprintf("/api/v1/drama/%s/episodes/%s/stream", seriesID, vid))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		resp.Body.Close()
		t.Fatalf("stream status = %d body = %s", resp.StatusCode, body)
	}
	body := decodeJSON(t, resp)
	proxyURL, _ := body["proxy_url"].(string)
	t.Logf("line=%v quality=%v duration_ms=%v vertical=%v cenc=%v hls=%v",
		body["line"], body["quality"], body["duration_ms"], body["vertical"], body["cenc"], body["hls"])
	if proxyURL == "" {
		t.Fatalf("proxy_url missing: %v", body)
	}

	// 2) 代理拉首段（Range）验证转发
	req, _ := http.NewRequest(http.MethodGet, ts.URL+proxyURL, nil)
	req.Header.Set("Range", "bytes=0-1023")
	media, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer media.Body.Close()
	first, _ := io.ReadAll(io.LimitReader(media.Body, 2<<10))
	t.Logf("proxy status=%d content-type=%s bytes=%d", media.StatusCode, media.Header.Get("Content-Type"), len(first))
	if media.StatusCode != http.StatusOK && media.StatusCode != http.StatusPartialContent {
		t.Fatalf("proxy status = %d", media.StatusCode)
	}

	// 3) 弹幕首窗（duration 用取流时长；0 时跳过）
	duration := int64(0)
	if d, ok := body["duration_ms"].(float64); ok {
		duration = int64(d)
	}
	if duration <= 0 {
		t.Skipf("弹幕冒烟跳过：duration_ms=%v", body["duration_ms"])
	}
	dresp, err := http.Get(ts.URL + fmt.Sprintf("/api/v1/drama/%s/episodes/%s/danmaku?from=0&duration=%d", seriesID, vid, duration))
	if err != nil {
		t.Fatal(err)
	}
	defer dresp.Body.Close()
	dbody := decodeJSON(t, dresp)
	items, _ := dbody["items"].([]any)
	next, _ := dbody["next_ms"].(float64)
	t.Logf("danmaku window items=%d next_ms=%v", len(items), next)
	if dresp.StatusCode != http.StatusOK || next <= 0 {
		t.Fatalf("danmaku status=%d next=%v", dresp.StatusCode, next)
	}
}
