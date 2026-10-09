package pipeline

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2017fighting/guo/internal/ass"
	"github.com/2017fighting/guo/internal/jellyfin"
	"github.com/2017fighting/guo/internal/layout"
	"github.com/2017fighting/guo/internal/store"
)

// ---- 测试基建：假源 + Range 文件服务器 + 假 ffmpeg + 假 Jellyfin ----

type fakeSource struct {
	meta    DramaMeta
	streams map[string]*Stream
	danmaku map[string][]ass.Comment
}

func (f *fakeSource) Detail(ctx context.Context, seriesID string) (*DramaMeta, error) {
	m := f.meta
	m.SeriesID = seriesID
	return &m, nil
}

func (f *fakeSource) ResolveStream(ctx context.Context, seriesID, vid string, quality int) (*Stream, error) {
	s, ok := f.streams[vid]
	if !ok {
		return nil, fmt.Errorf("no stream for %s", vid)
	}
	cp := *s
	return &cp, nil
}

func (f *fakeSource) DanmakuAll(ctx context.Context, seriesID, vid string, durationMS int64) ([]ass.Comment, error) {
	return f.danmaku[vid], nil
}

// fakeFFmpeg 按 args 里的 -i <in> ... <out> 直接拷贝文件（模拟 -c copy 合并）。
type fakeFFmpeg struct{ calls [][]string }

func (f *fakeFFmpeg) Run(ctx context.Context, args ...string) error {
	f.calls = append(f.calls, args)
	in, out := "", ""
	for i, a := range args {
		if a == "-i" && i+1 < len(args) {
			in = args[i+1]
		}
	}
	if len(args) > 0 {
		out = args[len(args)-1]
	}
	if in == "" || out == "" || in == out {
		return fmt.Errorf("bad args: %v", args)
	}
	return os.Rename(in, out)
}

func newMediaServer(t *testing.T, body []byte, hits *int32, expiredFirst *int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(hits, 1)
		// 首次请求模拟 CDN 签名过期，重取后放行
		if expiredFirst != nil && n == 1 {
			atomic.AddInt32(expiredFirst, 1)
			w.WriteHeader(http.StatusGone)
			return
		}
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

func newEngine(t *testing.T) (*Engine, *fakeSource, *fakeFFmpeg, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	root := t.TempDir()
	src := &fakeSource{}
	ff := &fakeFFmpeg{}
	e := &Engine{
		Store: st, Source: src, MediaRoot: root, FFMpeg: ff,
		ASSExport: true, Log: func(string) {},
	}
	return e, src, ff, root
}

func mustAdd(t *testing.T, e *Engine, id string, eps []int) *store.Job {
	t.Helper()
	j, err := e.AddJob(context.Background(), id, eps, 0)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

// ---- 用例 ----

func TestFullPipelineArtifacts(t *testing.T) {
	e, src, ff, root := newEngine(t)

	media := []byte(strings.Repeat("FAKEVIDEOBYTES-", 256))
	var hits int32
	msrv := newMediaServer(t, media, &hits, nil)
	var posterHits int32
	psrv := newMediaServer(t, []byte("JPEGDATA"), &posterHits, nil)

	src.meta = DramaMeta{
		Title: "测试剧", Year: "2025", Plot: "剧情简介", Genres: []string{"都市"},
		CoverURL: psrv.URL + "/cover.jpg",
		Episodes: []EpisodeInfo{{1, "v1"}, {2, "v2"}, {3, "v3"}},
	}
	src.streams = map[string]*Stream{
		"v1": {URL: msrv.URL + "/v1.mp4", CENCKeyHex: "aabb1122", DurationMS: 60000},
		"v2": {URL: msrv.URL + "/v2.mp4", DurationMS: 60000},
		"v3": {URL: msrv.URL + "/v3.mp4", DurationMS: 60000},
	}
	src.danmaku = map[string][]ass.Comment{
		"v1": {{ID: "d1", Text: "好看", TimeMS: 1500}},
	}

	mustAdd(t, e, "700001", nil) // 整剧
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	showDir := filepath.Join(root, "测试剧 (2025)")
	for _, p := range []string{
		"tvshow.nfo", "poster.jpg", "Season 01/S01E001.mp4",
		"Season 01/S01E001.nfo", "Season 01/S01E001.zh.ass",
		"Season 01/S02.mp4", // 不存在——防误报
	} {
		exists := fileExists(filepath.Join(showDir, filepath.FromSlash(p)))
		if p == "Season 01/S02.mp4" {
			if exists {
				t.Fatalf("unexpected file %s", p)
			}
			continue
		}
		if !exists {
			t.Errorf("missing %s", p)
		}
	}
	// 无弹幕的集不生成 .ass
	if fileExists(filepath.Join(showDir, "Season 01/S01E002.zh.ass")) {
		t.Error("v2 has no danmaku, .ass should be absent")
	}
	// .part 清理
	if fileExists(filepath.Join(showDir, "Season 01/S01E001.part")) {
		t.Error(".part not cleaned")
	}
	// 视频内容与假 ffmpeg 拷贝一致
	b, _ := os.ReadFile(filepath.Join(showDir, "Season 01/S01E001.mp4"))
	if string(b) != string(media) {
		t.Error("merged content mismatch")
	}
	// 带 key 的集：ffmpeg args 含 -decryption_key 且在 -i 前
	var sawKey bool
	for _, call := range ff.calls {
		for i, a := range call {
			if a == "-decryption_key" {
				sawKey = true
				inPos, keyPos := indexOf(call, "-i"), i
				if keyPos > inPos {
					t.Error("-decryption_key must precede -i")
				}
			}
		}
	}
	if !sawKey {
		t.Error("v1 stream had CENC key but ffmpeg args never set it")
	}
	// 状态终态
	j, _ := e.Store.GetJob("hongguo:700001")
	if j.Status != store.JobDone {
		t.Fatalf("job status = %s", j.Status)
	}
	eps, _ := e.Store.Episodes(j.ID)
	for _, ep := range eps {
		if ep.Status != store.EpDone {
			t.Fatalf("ep %d status = %s", ep.Index, ep.Status)
		}
	}
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

func TestResumeFromPartialPart(t *testing.T) {
	e, src, _, root := newEngine(t)
	media := []byte(strings.Repeat("X", 4096))
	var hits int32
	msrv := newMediaServer(t, media, &hits, nil)
	src.meta = DramaMeta{Title: "续传剧", Episodes: []EpisodeInfo{{1, "v1"}}}
	src.streams = map[string]*Stream{"v1": {URL: msrv.URL + "/m.mp4", DurationMS: 1000}}
	mustAdd(t, e, "42", []int{1})

	ep := layout.Episode{
		Dir:  filepath.Join(layout.SeasonDir(layout.ShowDir(root, "续传剧", ""), 1)),
		Stem: layout.EpisodeStem(1, 1),
	}
	os.MkdirAll(filepath.Dir(ep.Part()), 0o755)
	// 预置前 1000 字节的半成品
	if err := os.WriteFile(ep.Part(), media[:1000], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	// 断言只补了 1000 字节之后的部分：请求头带 Range
	b, _ := os.ReadFile(ep.Video())
	if string(b) != string(media) {
		t.Error("resumed content mismatch")
	}
	j, _ := e.Store.GetJob("hongguo:42")
	if j.Status != store.JobDone {
		t.Errorf("status = %s", j.Status)
	}
}

func TestURLExpiredReResolve(t *testing.T) {
	e, src, _, _ := newEngine(t)
	media := []byte(strings.Repeat("Y", 2048))
	var hits, expired int32
	msrv := newMediaServer(t, media, &hits, &expired)
	src.meta = DramaMeta{Title: "过期剧", Episodes: []EpisodeInfo{{1, "v1"}}}
	src.streams = map[string]*Stream{"v1": {URL: msrv.URL + "/m.mp4", DurationMS: 1000}}
	mustAdd(t, e, "43", []int{1})

	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	j, _ := e.Store.GetJob("hongguo:43")
	if j.Status != store.JobDone {
		t.Fatalf("status = %s (expired=%d hits=%d)", j.Status, expired, hits)
	}
}

func TestEpisodeFailureRetriesAndJobFailed(t *testing.T) {
	e, src, _, _ := newEngine(t)
	src.meta = DramaMeta{Title: "失败剧", Episodes: []EpisodeInfo{{1, "bad"}, {2, "good"}}}
	media := []byte("OK")
	var hits int32
	msrv := newMediaServer(t, media, &hits, nil)
	src.streams = map[string]*Stream{
		"bad":  nil, // ResolveStream 返回错误
		"good": {URL: msrv.URL + "/g.mp4", DurationMS: 1000},
	}
	// 覆盖 ResolveStream：bad 报错
	src2 := &failingSource{fakeSource: *src}
	e.Source = src2
	e.MaxRetries = 1 // 测试路径：不重试，加快用例

	mustAdd(t, e, "44", nil)
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	j, _ := e.Store.GetJob("hongguo:44")
	if j.Status != store.JobFailed {
		t.Fatalf("job status = %s", j.Status)
	}
	eps, _ := e.Store.Episodes(j.ID)
	if eps[0].Status != store.EpFailed || eps[0].Retries != 1 {
		t.Fatalf("ep1 = %+v", eps[0])
	}
	if eps[1].Status != store.EpDone {
		t.Fatalf("ep2 should still complete: %+v", eps[1])
	}
	// RetryJob 重置后重跑仍失败 → retries=2
	if err := e.RetryJob(j.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	eps, _ = e.Store.Episodes(j.ID)
	if eps[0].Retries != 2 {
		t.Fatalf("retries after rerun = %d", eps[0].Retries)
	}
}

type failingSource struct{ fakeSource }

func (f *failingSource) ResolveStream(ctx context.Context, seriesID, vid string, quality int) (*Stream, error) {
	if vid == "bad" {
		return nil, fmt.Errorf("upstream boom")
	}
	return f.fakeSource.ResolveStream(ctx, seriesID, vid, quality)
}

func TestJellyfinRefreshOnJobDone(t *testing.T) {
	e, src, _, _ := newEngine(t)
	var jfHits int32
	jf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&jfHits, 1)
		if r.Header.Get("Authorization") != `MediaBrowser Token="k"` {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(204)
	}))
	defer jf.Close()
	e.Jellyfin = &jellyfin.Client{BaseURL: jf.URL, APIKey: "k"}
	media := []byte("Z")
	var hits int32
	msrv := newMediaServer(t, media, &hits, nil)
	src.meta = DramaMeta{Title: "刷新剧", Episodes: []EpisodeInfo{{1, "v1"}}}
	src.streams = map[string]*Stream{"v1": {URL: msrv.URL + "/z.mp4", DurationMS: 1000}}
	mustAdd(t, e, "45", []int{1})
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for atomic.LoadInt32(&jfHits) == 0 {
		select {
		case <-deadline:
			t.Fatal("jellyfin refresh never fired")
		default:
		}
	}
}

func TestDeleteJobKeepVideo(t *testing.T) {
	e, src, _, root := newEngine(t)
	media := []byte("D")
	var hits int32
	msrv := newMediaServer(t, media, &hits, nil)
	src.meta = DramaMeta{Title: "删除剧", Episodes: []EpisodeInfo{{1, "v1"}}}
	src.streams = map[string]*Stream{"v1": {URL: msrv.URL + "/d.mp4", DurationMS: 1000}}
	mustAdd(t, e, "46", []int{1})
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	showDir := filepath.Join(root, "删除剧")
	if err := e.DeleteJob("hongguo:46", true); err != nil {
		t.Fatal(err)
	}
	if !fileExists(filepath.Join(showDir, "Season 01/S01E001.mp4")) {
		t.Fatal("keepVideo should retain files")
	}
	if _, err := e.Store.GetJob("hongguo:46"); err != store.ErrNotFound {
		t.Fatal("job not deleted")
	}
	if err := e.DeleteJob("hongguo:46", false); err == nil {
		t.Fatal("second delete should 404")
	}
}

func TestURLExpiredReResolveUsesFreshKey(t *testing.T) {
	e, src, ff, _ := newEngine(t)
	media := []byte(strings.Repeat("K", 1024))
	var hits, expired int32
	msrv := newMediaServer(t, media, &hits, &expired)
	src.meta = DramaMeta{Title: "换钥剧", Episodes: []EpisodeInfo{{1, "v1"}}}
	// 首次解析：带旧 key；URL 首清 410，重取后换新 key（模拟换线路换密钥）
	src.streams = map[string]*Stream{"v1": {URL: msrv.URL + "/m.mp4", CENCKeyHex: "00000000000000000000000000000000", DurationMS: 1000}}
	flippy := &keyFlipSource{fakeSource: *src, newKey: "ffffffffffffffffffffffffffffffff"}
	e.Source = flippy
	mustAdd(t, e, "47", []int{1})
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	j, _ := e.Store.GetJob("hongguo:47")
	if j.Status != store.JobDone {
		t.Fatalf("status = %s", j.Status)
	}
	sawNew, sawOld := false, false
	for _, call := range ff.calls {
		for i, a := range call {
			if a == "-decryption_key" && i+1 < len(call) {
				switch call[i+1] {
				case "ffffffffffffffffffffffffffffffff":
					sawNew = true
				case "00000000000000000000000000000000":
					sawOld = true
				}
			}
		}
	}
	if !sawNew || sawOld {
		t.Fatalf("ffmpeg key stale: new=%v old=%v", sawNew, sawOld)
	}
}

type keyFlipSource struct {
	fakeSource
	newKey   string
	resolved int32
}

func (k *keyFlipSource) ResolveStream(ctx context.Context, seriesID, vid string, quality int) (*Stream, error) {
	n := atomic.AddInt32(&k.resolved, 1)
	s, err := k.fakeSource.ResolveStream(ctx, seriesID, vid, quality)
	if err != nil {
		return nil, err
	}
	if n > 1 { // 第二次解析（重取）换新 key
		s.CENCKeyHex = k.newKey
	}
	return s, nil
}

func TestQualityPersisted(t *testing.T) {
	e, src, _, _ := newEngine(t)
	media := []byte("Q")
	var hits int32
	msrv := newMediaServer(t, media, &hits, nil)
	src.meta = DramaMeta{Title: "画质剧", Episodes: []EpisodeInfo{{1, "v1"}}}
	src.streams = map[string]*Stream{"v1": {URL: msrv.URL + "/q.mp4", DurationMS: 1000}}
	j, err := e.AddJob(context.Background(), "48", []int{1}, 720)
	if err != nil {
		t.Fatal(err)
	}
	if j.Quality != 720 {
		t.Fatalf("quality = %d", j.Quality)
	}
	got, _ := e.Store.GetJob("hongguo:48")
	if got.Quality != 720 {
		t.Fatalf("persisted quality = %d", got.Quality)
	}
}

func TestDoneJobRequeuesOnNewEpisodes(t *testing.T) {
	e, src, _, _ := newEngine(t)
	media := []byte("R")
	var hits int32
	msrv := newMediaServer(t, media, &hits, nil)
	src.meta = DramaMeta{Title: "更新剧", Episodes: []EpisodeInfo{{1, "v1"}}}
	src.streams = map[string]*Stream{"v1": {URL: msrv.URL + "/r1.mp4", DurationMS: 1000}}
	mustAdd(t, e, "49", []int{1})
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	j, _ := e.Store.GetJob("hongguo:49")
	if j.Status != store.JobDone {
		t.Fatalf("precondition: status = %s", j.Status)
	}
	// 「更新本剧」：源上新了第 2 集，重新 add 后任务应自动回队列
	src.meta.Episodes = append(src.meta.Episodes, EpisodeInfo{2, "v2"})
	src.streams["v2"] = &Stream{URL: msrv.URL + "/r2.mp4", DurationMS: 1000}
	j2, err := e.AddJob(context.Background(), "49", []int{2}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if j2.Status != store.JobQueued {
		t.Fatalf("requeued status = %s", j2.Status)
	}
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	j3, _ := e.Store.GetJob("hongguo:49")
	if j3.Status != store.JobDone {
		t.Fatalf("final status = %s", j3.Status)
	}
	if !fileExists(filepath.Join(e.MediaRoot, "更新剧", "Season 01", "S01E002.mp4")) {
		t.Fatal("new episode not downloaded")
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
