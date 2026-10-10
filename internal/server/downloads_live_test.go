package server

// 真机冒烟：GUO_LIVE=1 时才执行（不打进常规测试/CI）。
// 全链路：真源详情 → 建任务（常驻执行器）→ 下载完成 → 卡片聚合字段。
// 需要外网；GUO_LIVE_SERIES 可指定剧（默认取目录首位）。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2017fighting/guo/internal/hongguo"
	"github.com/2017fighting/guo/internal/pipeline"
	"github.com/2017fighting/guo/internal/store"
)

func TestDownloadsLiveSmoke(t *testing.T) {
	if os.Getenv("GUO_LIVE") != "1" {
		t.Skip("GUO_LIVE=1 才跑真机冒烟")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
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

	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	engine := &pipeline.Engine{
		Store: st, Source: source, MediaRoot: root,
		FFMpeg:    &pipeline.ExecRunner{Path: envOr("GUO_FFMPEG", "ffmpeg")},
		ASSExport: true, Log: func(m string) { t.Log(m) },
	}
	runner := pipeline.NewJobRunner(engine)
	rctx, rcancel := context.WithCancel(ctx)
	runner.Start(rctx)
	defer func() { rcancel(); runner.Stop() }()

	srv := &Server{Drama: source, Downloads: runner}
	hub := NewEventHub(srv.QueueSnapshot)
	srv.Events = hub
	engine.OnEvent = hub.Signal
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 详情（App→Web 回落）
	resp, err := http.Get(ts.URL + "/api/v1/drama/" + seriesID)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("详情失败: %v status=%d", err, resp.StatusCode)
	}
	var detail struct {
		Title    string `json:"title"`
		Episodes []struct {
			Index int    `json:"index"`
			VID   string `json:"vid"`
		} `json:"episodes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(detail.Episodes) < 2 {
		t.Fatalf("真机分集不足: %d", len(detail.Episodes))
	}
	t.Logf("真机剧: %s（%s，%d 集）", detail.Title, seriesID, len(detail.Episodes))

	// 建任务：前 2 集（控制真机下载量）
	body := `{"series_id":"` + seriesID + `","quality":0,"episodes":[1,2]}`
	create, err := http.Post(ts.URL+"/api/v1/downloads", "application/json", strings.NewReader(body))
	if err != nil || create.StatusCode != http.StatusOK {
		t.Fatalf("建任务失败: %v status=%d", err, create.StatusCode)
	}
	create.Body.Close()

	// 轮询卡片到 done（SSE 已有专门用例；此处走轮询口径）
	deadline := time.Now().Add(8 * time.Minute)
	for {
		job := pollLiveCard(t, ts)
		if job["status"] == "done" {
			t.Logf("完成卡片: %v", job)
			break
		}
		if job["status"] == "failed" {
			t.Fatalf("真机下载失败: %v", job)
		}
		if time.Now().After(deadline) {
			t.Fatalf("超时未完成: %v", job)
		}
		time.Sleep(3 * time.Second)
	}

	// 卡片终态字段
	final := pollLiveCard(t, ts)
	if final["done_episodes"] != float64(2) || final["current_episode"] != float64(0) {
		t.Errorf("终态卡片 = %v", final)
	}
	// 成品落盘
	jobs, _ := st.ListJobs()
	if len(jobs) != 1 {
		t.Fatalf("任务数 = %d", len(jobs))
	}
	mp4 := filepath.Join(root, fmt.Sprintf("%s (%s)", jobs[0].Title, jobs[0].Year), "Season 01", "S01E001.mp4")
	if _, err := os.Stat(mp4); err != nil {
		t.Errorf("成品缺失 %s: %v", mp4, err)
	} else {
		t.Logf("成品: %s", mp4)
	}
}

func pollLiveCard(t *testing.T, ts *httptest.Server) map[string]any {
	t.Helper()
	resp, err := http.Get(ts.URL + "/api/v1/downloads")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var snap struct {
		Jobs []map[string]any `json:"jobs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Jobs) == 0 {
		t.Fatal("队列空")
	}
	return snap.Jobs[0]
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
