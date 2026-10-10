package server

// /api/v1/downloads 下载队列路由用例：建任务（幂等）、聚合卡片、分集明细、
// 控制动作、删除（保留视频）。真管线 + 假源（httptest 媒体服务器 + 假 ffmpeg）。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2017fighting/guo/internal/ass"
	"github.com/2017fighting/guo/internal/pipeline"
	"github.com/2017fighting/guo/internal/store"
)

// ---- 测试基建：可控闸的假源 + 媒体服务器 ----

// fakeDramaSource 剧集字典假源；hold 的 vid 在取流处阻塞，open 后放行。
type fakeDramaSource struct {
	metas   map[string]pipeline.DramaMeta
	streams map[string]*pipeline.Stream
	mu      sync.Mutex
	hold    map[string]bool
	waiters map[string]chan struct{}
}

func newFakeDramaSource() *fakeDramaSource {
	return &fakeDramaSource{
		metas:   map[string]pipeline.DramaMeta{},
		streams: map[string]*pipeline.Stream{},
		hold:    map[string]bool{},
		waiters: map[string]chan struct{}{},
	}
}

func (f *fakeDramaSource) Detail(ctx context.Context, seriesID string) (*pipeline.DramaMeta, error) {
	m, ok := f.metas[seriesID]
	if !ok {
		return nil, fmt.Errorf("剧集 %s 不存在", seriesID)
	}
	cp := m
	cp.SeriesID = seriesID
	return &cp, nil
}

func (f *fakeDramaSource) holdVID(vid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hold[vid] = true
}

func (f *fakeDramaSource) openVID(vid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.hold, vid)
	if ch, ok := f.waiters[vid]; ok {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
}

func (f *fakeDramaSource) ResolveStream(ctx context.Context, seriesID, vid string, quality int) (*pipeline.Stream, error) {
	f.mu.Lock()
	blocked := f.hold[vid]
	var ch chan struct{}
	if blocked {
		var ok bool
		if ch, ok = f.waiters[vid]; !ok {
			ch = make(chan struct{})
			f.waiters[vid] = ch
		}
	}
	f.mu.Unlock()
	if blocked {
		select {
		case <-ch:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	s, ok := f.streams[vid]
	if !ok {
		return nil, fmt.Errorf("no stream for %s", vid)
	}
	cp := *s
	return &cp, nil
}

func (f *fakeDramaSource) DanmakuAll(ctx context.Context, seriesID, vid string, durationMS int64) ([]ass.Comment, error) {
	return nil, nil
}

// renameFFmpeg 假 ffmpeg：.part → 成品。
type renameFFmpeg struct{ calls int32 }

func (r *renameFFmpeg) Run(ctx context.Context, args ...string) error {
	atomic.AddInt32(&r.calls, 1)
	in, out := "", ""
	for i, a := range args {
		if a == "-i" && i+1 < len(args) {
			in = args[i+1]
		}
	}
	if len(args) > 0 {
		out = args[len(args)-1]
	}
	if in == "" || out == "" {
		return fmt.Errorf("bad args: %v", args)
	}
	return os.Rename(in, out)
}

type queueFixtureOpts struct {
	gateFirst bool // 闸住每部剧第 1 集的取流，测试暂停/控制
}

type queueFixture struct {
	ts        *httptest.Server
	engine    *pipeline.Engine
	runner    *pipeline.JobRunner
	source    *fakeDramaSource
	st        *store.Store
	hub       *EventHub
	mediaRoot string
	media     []byte
	mediaHits int32
	cancel    context.CancelFunc
	t         *testing.T
}

// newQueueFixture 起一套真引擎 + 常驻执行器 + HTTP 服务（默认并发 1，可控）。
func newQueueFixture(t *testing.T, opts queueFixtureOpts) *queueFixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	src := newFakeDramaSource()
	ff := &renameFFmpeg{}
	e := &pipeline.Engine{
		Store: st, Source: src, MediaRoot: root, FFMpeg: ff,
		ASSExport: true, Concurrency: 1, MaxRetries: 1, Log: func(string) {},
	}

	f := &queueFixture{source: src, st: st, engine: e, mediaRoot: root, t: t}
	f.media = []byte(strings.Repeat("FAKE", 512))
	msrv := newMediaRangeServer(t, f.media, &f.mediaHits)
	f.registerSeries("700001", "测试剧A", 3) // v7000011..v7000013
	f.registerSeries("700002", "测试剧B", 2) // v7000021..v7000022
	f.registerSeries("700003", "坏流剧", 2)  // 第 1 集无流（失败路径）
	for _, s := range []struct{ id, vid string }{
		{"700001", "v7000011"}, {"700001", "v7000012"}, {"700001", "v7000013"},
		{"700002", "v7000021"}, {"700002", "v7000022"},
		{"700003", "v7000032"},
	} {
		src.streams[s.vid] = &pipeline.Stream{URL: msrv.URL + "/" + s.vid + ".mp4", DurationMS: 1000}
	}
	if opts.gateFirst {
		src.holdVID("v7000011")
		src.holdVID("v7000021")
	}

	runner := pipeline.NewJobRunner(e)
	rctx, cancel := context.WithCancel(context.Background())
	runner.Start(rctx)

	srv := &Server{Drama: src, Downloads: runner}
	hub := NewEventHub(srv.QueueSnapshot)
	e.OnEvent = hub.Signal
	srv.Events = hub
	f.runner = runner
	f.hub = hub
	f.cancel = cancel
	f.ts = httptestUnstarted(srv)
	return f
}

func (f *queueFixture) registerSeries(id, title string, eps int) {
	f.source.metas[id] = pipeline.DramaMeta{
		Title: title, Year: "2025", Plot: title + " 的剧情简介",
		Genres:   []string{"都市", "逆袭"},
		CoverURL: "https://img.example/" + id + ".jpg",
	}
	meta := f.source.metas[id]
	for i := 1; i <= eps; i++ {
		meta.Episodes = append(meta.Episodes, pipeline.EpisodeInfo{Index: i, VID: fmt.Sprintf("v%s%d", id, i)})
	}
	f.source.metas[id] = meta
}

func (f *queueFixture) Close() {
	f.ts.Close()
	f.cancel()
	f.runner.Stop()
	f.st.Close()
}

// httptestUnstarted 直接从 Server 构造测试实例（不走 newTestServer 的目录桩）。
func httptestUnstarted(srv *Server) *httptest.Server {
	ts := httptest.NewServer(srv.Handler())
	return ts
}

func newMediaRangeServer(t *testing.T, body []byte, hits *int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		if rng := r.Header.Get("Range"); rng != "" {
			var start int64
			fmt.Sscanf(strings.TrimPrefix(rng, "bytes="), "%d-", &start)
			if start >= int64(len(body)) {
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(body)-1, len(body)))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(body[start:])
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *queueFixture) postJSON(t *testing.T, path string, payload any) *http.Response {
	t.Helper()
	data, _ := json.Marshal(payload)
	resp, err := http.Post(f.ts.URL+path, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func (f *queueFixture) waitJobStatus(t *testing.T, dramaID, want string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		j, err := f.st.GetJob(dramaID)
		if err == nil && j.Status == want {
			return
		}
		select {
		case <-deadline:
			j, _ := f.st.GetJob(dramaID)
			got := "missing"
			if j != nil {
				got = j.Status
			}
			t.Fatalf("任务 %s 状态 %q，等 %q 超时", dramaID, got, want)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// ---- POST /api/v1/downloads：幂等建任务 + done 任务补集重入队 ----

func TestDownloadsCreateIdempotent(t *testing.T) {
	f := newQueueFixture(t, queueFixtureOpts{})
	defer f.Close()

	resp := f.postJSON(t, "/api/v1/downloads", map[string]any{
		"series_id": "700001", "quality": 0, "episodes": []int{1, 2},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	card := decodeJSON(t, resp)
	if card["id"] != float64(1) || card["series_id"] != "700001" {
		t.Errorf("card = %v", card)
	}

	// 再建一次：同任务幂等返回
	resp2 := f.postJSON(t, "/api/v1/downloads", map[string]any{
		"series_id": "700001", "quality": 0, "episodes": []int{1, 2, 3},
	})
	defer resp2.Body.Close()
	card2 := decodeJSON(t, resp2)
	if card2["id"] != card["id"] {
		t.Errorf("幂等失败: %v vs %v", card["id"], card2["id"])
	}

	f.waitJobStatus(t, "hongguo:700001", store.JobDone)
}

func TestDownloadsCreateValidation(t *testing.T) {
	f := newQueueFixture(t, queueFixtureOpts{})
	defer f.Close()

	cases := []struct {
		name    string
		payload any
		message string
	}{
		{"缺 series_id", map[string]any{"episodes": []int{1}}, "请求参数不对"},
		{"series_id 非数字", map[string]any{"series_id": "abc"}, "请求参数不对"},
		{"分集号非正数", map[string]any{"series_id": "700001", "episodes": []int{0}}, "请求参数不对"},
		{"画质档为负", map[string]any{"series_id": "700001", "quality": -1}, "请求参数不对"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := f.postJSON(t, "/api/v1/downloads", tc.payload)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			if body := decodeJSON(t, resp); body["message"] != tc.message {
				t.Errorf("message = %v", body["message"])
			}
		})
	}
}

func TestDownloadsCreateUpstreamFailure(t *testing.T) {
	f := newQueueFixture(t, queueFixtureOpts{})
	defer f.Close()
	resp := f.postJSON(t, "/api/v1/downloads", map[string]any{"series_id": "999999"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if body := decodeJSON(t, resp); body["message"] != "创建下载任务失败，请稍后重试" {
		t.Errorf("message = %v", body["message"])
	}
}

// ---- GET /api/v1/downloads：卡片聚合（三态字段） ----

func TestDownloadsListAggregation(t *testing.T) {
	f := newQueueFixture(t, queueFixtureOpts{})
	defer f.Close()

	// 700001 完成；700003 第 1 集失败（无流）
	resp := f.postJSON(t, "/api/v1/downloads", map[string]any{"series_id": "700001", "episodes": []int{1, 2}})
	resp.Body.Close()
	resp = f.postJSON(t, "/api/v1/downloads", map[string]any{"series_id": "700003", "episodes": []int{1, 2}})
	resp.Body.Close()
	f.waitJobStatus(t, "hongguo:700001", store.JobDone)
	f.waitJobStatus(t, "hongguo:700003", store.JobFailed)

	list, err := http.Get(f.ts.URL + "/api/v1/downloads")
	if err != nil {
		t.Fatal(err)
	}
	defer list.Body.Close()
	if list.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", list.StatusCode)
	}
	body := decodeJSON(t, list)
	jobs, _ := body["jobs"].([]any)
	if len(jobs) != 2 {
		t.Fatalf("jobs = %v", body)
	}
	byID := map[float64]map[string]any{}
	for _, j := range jobs {
		m, _ := j.(map[string]any)
		byID[m["id"].(float64)] = m
	}
	done := byID[1]
	if done["status"] != store.JobDone || done["total_episodes"] != float64(2) || done["done_episodes"] != float64(2) {
		t.Errorf("done card = %v", done)
	}
	if done["downloaded_bytes"] != float64(len(f.media)*2) {
		t.Errorf("downloaded_bytes = %v, want %d", done["downloaded_bytes"], len(f.media)*2)
	}
	if done["speed_bps"] != float64(0) || done["current_episode"] != float64(0) {
		t.Errorf("idle live fields = %v/%v", done["speed_bps"], done["current_episode"])
	}
	failed := byID[2]
	if failed["status"] != store.JobFailed || failed["failed_episodes"] != float64(1) || failed["done_episodes"] != float64(1) {
		t.Errorf("failed card = %v", failed)
	}
}

func TestDownloadsListRunning(t *testing.T) {
	f := newQueueFixture(t, queueFixtureOpts{gateFirst: true})
	defer f.Close()

	resp := f.postJSON(t, "/api/v1/downloads", map[string]any{"series_id": "700002", "episodes": []int{1, 2}})
	resp.Body.Close()
	waitEpisodeStatus(t, f, "hongguo:700002", 1, store.EpDownloading)

	list, err := http.Get(f.ts.URL + "/api/v1/downloads")
	if err != nil {
		t.Fatal(err)
	}
	defer list.Body.Close()
	body := decodeJSON(t, list)
	jobs, _ := body["jobs"].([]any)
	if len(jobs) != 1 {
		t.Fatalf("jobs = %v", body)
	}
	card, _ := jobs[0].(map[string]any)
	if card["status"] != store.JobRunning || card["current_episode"] != float64(1) {
		t.Errorf("running card = %v", card)
	}
	f.source.openVID("v7000021")
	f.waitJobStatus(t, "hongguo:700002", store.JobDone)
}

func waitEpisodeStatus(t *testing.T, f *queueFixture, dramaID string, idx int, want string) {
	t.Helper()
	j, err := f.st.GetJob(dramaID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		eps, _ := f.st.Episodes(j.ID)
		for _, ep := range eps {
			if ep.Index == idx && ep.Status == want {
				return
			}
		}
		select {
		case <-deadline:
			t.Fatalf("分集 %d 未进入 %s", idx, want)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// ---- GET /downloads/{id}/episodes：分集明细（含失败原因） ----

func TestDownloadEpisodesList(t *testing.T) {
	f := newQueueFixture(t, queueFixtureOpts{})
	defer f.Close()

	resp := f.postJSON(t, "/api/v1/downloads", map[string]any{"series_id": "700003", "episodes": []int{1, 2}})
	card := decodeJSON(t, resp)
	resp.Body.Close()
	jobID := int64(card["id"].(float64))
	f.waitJobStatus(t, "hongguo:700003", store.JobFailed)

	list, err := http.Get(f.ts.URL + fmt.Sprintf("/api/v1/downloads/%d/episodes", jobID))
	if err != nil {
		t.Fatal(err)
	}
	defer list.Body.Close()
	if list.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", list.StatusCode)
	}
	body := decodeJSON(t, list)
	if body["job_id"] != float64(jobID) {
		t.Errorf("job_id = %v", body["job_id"])
	}
	eps, _ := body["episodes"].([]any)
	if len(eps) != 2 {
		t.Fatalf("episodes = %v", body)
	}
	ep1, _ := eps[0].(map[string]any)
	if ep1["status"] != store.EpFailed || ep1["retries"] != float64(1) {
		t.Errorf("ep1 = %v", ep1)
	}
	if msg, _ := ep1["error"].(string); msg == "" {
		t.Errorf("failed episode missing error: %v", ep1)
	}
	ep2, _ := eps[1].(map[string]any)
	if ep2["status"] != store.EpDone || ep2["vid"] != "v7000032" {
		t.Errorf("ep2 = %v", ep2)
	}
}

func TestDownloadEpisodesNotFound(t *testing.T) {
	f := newQueueFixture(t, queueFixtureOpts{})
	defer f.Close()
	resp, err := http.Get(f.ts.URL + "/api/v1/downloads/99/episodes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if body := decodeJSON(t, resp); body["message"] != "没有这个下载任务" {
		t.Errorf("message = %v", body["message"])
	}
}

// ---- 控制动作：暂停/恢复/重试 ----

func TestDownloadsPauseResumeRetry(t *testing.T) {
	f := newQueueFixture(t, queueFixtureOpts{gateFirst: true})
	defer f.Close()

	resp := f.postJSON(t, "/api/v1/downloads", map[string]any{"series_id": "700002", "episodes": []int{1, 2}})
	card := decodeJSON(t, resp)
	resp.Body.Close()
	jobID := int64(card["id"].(float64))

	// 下载中暂停 → 分集边界生效 → paused
	waitEpisodeStatus(t, f, "hongguo:700002", 1, store.EpDownloading)
	resp = f.postJSON(t, fmt.Sprintf("/api/v1/downloads/%d/pause", jobID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pause status = %d", resp.StatusCode)
	}
	paused := decodeJSON(t, resp)
	resp.Body.Close()
	if paused["status"] != store.JobPaused {
		t.Errorf("pause card = %v", paused)
	}
	f.source.openVID("v7000021")
	f.waitJobStatus(t, "hongguo:700002", store.JobPaused)

	// 恢复 → 排队 → 完成
	resp = f.postJSON(t, fmt.Sprintf("/api/v1/downloads/%d/resume", jobID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resume status = %d", resp.StatusCode)
	}
	resp.Body.Close()
	f.waitJobStatus(t, "hongguo:700002", store.JobDone)

	// 完成后 retry 幂等无害
	resp = f.postJSON(t, fmt.Sprintf("/api/v1/downloads/%d/retry", jobID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("retry status = %d", resp.StatusCode)
	}
	resp.Body.Close()
	f.waitJobStatus(t, "hongguo:700002", store.JobDone)
}

func TestDownloadsControlNotFound(t *testing.T) {
	f := newQueueFixture(t, queueFixtureOpts{})
	defer f.Close()
	for _, action := range []string{"pause", "resume", "retry"} {
		resp := f.postJSON(t, "/api/v1/downloads/42/"+action, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status = %d", action, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

// ---- 删除：确认语义参数 keepVideo ----

func TestDownloadsDelete(t *testing.T) {
	f := newQueueFixture(t, queueFixtureOpts{})
	defer f.Close()

	resp := f.postJSON(t, "/api/v1/downloads", map[string]any{"series_id": "700001", "episodes": []int{1}})
	card := decodeJSON(t, resp)
	resp.Body.Close()
	jobID := int64(card["id"].(float64))
	f.waitJobStatus(t, "hongguo:700001", store.JobDone)
	video := filepath.Join(f.mediaRoot, "测试剧A (2025)", "Season 01", "S01E001.mp4")
	if _, err := os.Stat(video); err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/downloads/%d?keepVideo=1", f.ts.URL, jobID), nil)
	dresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	dresp.Body.Close()
	if dresp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", dresp.StatusCode)
	}
	if _, err := os.Stat(video); err != nil {
		t.Fatalf("keepVideo 应保留视频: %v", err)
	}
	if _, err := f.st.GetJob("hongguo:700001"); err != store.ErrNotFound {
		t.Fatal("job not deleted")
	}
	// 二次删除 404
	req2, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/downloads/%d", f.ts.URL, jobID), nil)
	dresp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	dresp2.Body.Close()
	if dresp2.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete status = %d", dresp2.StatusCode)
	}
}

// 未配置下载引擎时给出可读错误。
func TestDownloadsNotConfigured(t *testing.T) {
	srv := &Server{}
	ts := httptestUnstarted(srv)
	defer ts.Close()
	resp := f0(srv)
	_ = resp
	list, err := http.Get(ts.URL + "/api/v1/downloads")
	if err != nil {
		t.Fatal(err)
	}
	defer list.Body.Close()
	if list.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", list.StatusCode)
	}
}

func f0(*Server) int { return 0 }
