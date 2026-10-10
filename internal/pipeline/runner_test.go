package pipeline

// JobRunner 常驻执行器用例：启动排空/唤醒/空闲、断电重启续传不重拉、
// 全局并发上限、分集边界暂停恢复、删除运行中任务即停。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2017fighting/guo/internal/ass"
	"github.com/2017fighting/guo/internal/layout"
	"github.com/2017fighting/guo/internal/store"
)

// gateSource 可按 vid 闸住 ResolveStream 的假源：hold 的 vid 在取流处阻塞，
// open 后放行；并发峰值按「进入 ResolveStream 未返回」计（被闸住也算在跑）。
type gateSource struct {
	fakeSource
	mu         sync.Mutex
	hold       map[string]bool
	waiters    map[string]chan struct{}
	seriesMeta map[string]DramaMeta // 可选：按 seriesID 区分剧集（同名目录隔离）
	inFlight   int32
	maxIn      int32
}

func newGateSource() *gateSource {
	return &gateSource{hold: map[string]bool{}, waiters: map[string]chan struct{}{}, seriesMeta: map[string]DramaMeta{}}
}

func (g *gateSource) Detail(ctx context.Context, seriesID string) (*DramaMeta, error) {
	if m, ok := g.seriesMeta[seriesID]; ok {
		cp := m
		cp.SeriesID = seriesID
		return &cp, nil
	}
	return g.fakeSource.Detail(ctx, seriesID)
}

func (g *gateSource) holdVID(vid string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.hold[vid] = true
}

func (g *gateSource) open(vid string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.hold, vid)
	if ch, ok := g.waiters[vid]; ok {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
}

func (g *gateSource) ResolveStream(ctx context.Context, seriesID, vid string, quality int) (*Stream, error) {
	n := atomic.AddInt32(&g.inFlight, 1)
	for {
		max := atomic.LoadInt32(&g.maxIn)
		if n <= max || atomic.CompareAndSwapInt32(&g.maxIn, max, n) {
			break
		}
	}
	defer atomic.AddInt32(&g.inFlight, -1)
	g.mu.Lock()
	blocked := g.hold[vid]
	var ch chan struct{}
	if blocked {
		var ok bool
		if ch, ok = g.waiters[vid]; !ok {
			ch = make(chan struct{})
			g.waiters[vid] = ch
		}
	}
	g.mu.Unlock()
	if blocked {
		select {
		case <-ch:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return g.fakeSource.ResolveStream(ctx, seriesID, vid, quality)
}

func (g *gateSource) maxConcurrent() int32 { return atomic.LoadInt32(&g.maxIn) }

func gateEngine(t *testing.T, concurrency int) (*Engine, *gateSource, string) {
	t.Helper()
	e, _, _, root := newEngine(t)
	g := newGateSource()
	e.Source = g
	e.Concurrency = concurrency
	return e, g, root
}

func waitStatus(t *testing.T, e *Engine, dramaID, want string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		j, err := e.Store.GetJob(dramaID)
		if err == nil && j.Status == want {
			return
		}
		select {
		case <-deadline:
			j, _ := e.Store.GetJob(dramaID)
			status := ""
			if j != nil {
				status = j.Status
			}
			t.Fatalf("任务 %s 状态一直是 %q，等 %q 超时", dramaID, status, want)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// 启动即排空遗留队列；空闲后被再次唤醒也能继续干活。
func TestJobRunnerStartupDrainAndWakeFromIdle(t *testing.T) {
	e, g, _ := gateEngine(t, 2)
	media := []byte("M")
	var hits int32
	msrv := newMediaServer(t, media, &hits, nil)
	g.fakeSource.meta = DramaMeta{Title: "常驻剧", Episodes: []EpisodeInfo{{1, "v1"}, {2, "v2"}}}
	g.fakeSource.streams = map[string]*Stream{
		"v1": {URL: msrv.URL + "/1.mp4", DurationMS: 1000},
		"v2": {URL: msrv.URL + "/2.mp4", DurationMS: 1000},
	}

	// 全部数据先声明（常驻运行期不改共享 fixtures）
	g.fakeSource.meta = DramaMeta{Title: "常驻剧", Episodes: []EpisodeInfo{{1, "v1"}, {2, "v2"}}}
	g.fakeSource.streams = map[string]*Stream{
		"v1": {URL: msrv.URL + "/1.mp4", DurationMS: 1000},
		"v2": {URL: msrv.URL + "/2.mp4", DurationMS: 1000},
	}

	// 先建任务（引擎直连，模拟上一进程遗留），再启动常驻执行器
	if _, err := e.AddJob(context.Background(), "700100", nil, 0); err != nil {
		t.Fatal(err)
	}
	r := NewJobRunner(e)
	rctx, cancel := context.WithCancel(context.Background())
	r.Start(rctx)
	defer func() { cancel(); r.Stop() }()
	waitStatus(t, e, "hongguo:700100", store.JobDone)

	// 空闲后新建任务（复用已声明的剧集数据）：应被唤醒并完成
	if _, err := r.AddJob(context.Background(), "700101", []int{1}, 0); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, "hongguo:700101", store.JobDone)
}

// 断电重启：running 卡死任务回到队列、分集回 pending，.part 字节续传不重拉。
func TestJobRunnerRestartResumesWithoutRepull(t *testing.T) {
	e, g, root := gateEngine(t, 2)
	media := []byte("RESTART")
	var hits int32
	msrv := newMediaServer(t, media, &hits, nil)
	g.fakeSource.meta = DramaMeta{Title: "重启剧", Episodes: []EpisodeInfo{{1, "v1"}}}
	g.fakeSource.streams = map[string]*Stream{"v1": {URL: msrv.URL + "/v1.mp4", DurationMS: 1000}}

	job, err := e.AddJob(context.Background(), "700102", []int{1}, 0)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟上次进程崩溃现场：任务 running、分集 downloading、.part 已有 4 字节
	if err := e.Store.SetJobStatus(job.ID, store.JobRunning); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.SetEpisodeStatus(job.ID, 1, store.EpDownloading); err != nil {
		t.Fatal(err)
	}
	ep := layout.Episode{
		Dir:  filepath.Join(layout.SeasonDir(layout.ShowDir(root, "重启剧", ""), 1)),
		Stem: layout.EpisodeStem(1, 1),
	}
	os.MkdirAll(filepath.Dir(ep.Part()), 0o755)
	if err := os.WriteFile(ep.Part(), media[:4], 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewJobRunner(e)
	rctx, cancel := context.WithCancel(context.Background())
	r.Start(rctx)
	defer func() { cancel(); r.Stop() }()
	waitStatus(t, e, "hongguo:700102", store.JobDone)

	// 续传不重拉：只有一次 Range 请求（起始 4 字节），且成品 = 预置 4 字节 + 后续
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("媒体请求数 = %d，应恰好 1 次（Range 续传）", n)
	}
	b, _ := os.ReadFile(ep.Video())
	if string(b) != string(media) {
		t.Fatalf("续传内容不完整: %q", b)
	}
}

// 全局并发上限：3 个任务、并发 2，任意时刻在跑任务 ≤ 2。
func TestJobRunnerRespectsGlobalConcurrency(t *testing.T) {
	e, g, _ := gateEngine(t, 2)
	media := []byte("C")
	var hits int32
	msrv := newMediaServer(t, media, &hits, nil)
	g.fakeSource.meta = DramaMeta{Title: "并发剧", Episodes: []EpisodeInfo{{1, "a"}, {2, "b"}, {3, "c"}}}
	g.fakeSource.streams = map[string]*Stream{
		"a": {URL: msrv.URL + "/a.mp4", DurationMS: 1000},
		"b": {URL: msrv.URL + "/b.mp4", DurationMS: 1000},
		"c": {URL: msrv.URL + "/c.mp4", DurationMS: 1000},
	}
	for _, vid := range []string{"a", "b", "c"} {
		g.holdVID(vid)
	}

	r := NewJobRunner(e)
	rctx, cancel := context.WithCancel(context.Background())
	r.Start(rctx)
	defer func() { cancel(); r.Stop() }()
	for _, id := range []string{"700110", "700111", "700112"} {
		if _, err := r.AddJob(context.Background(), id, []int{1}, 0); err != nil {
			t.Fatal(err)
		}
	}
	// 等并发拉起（第 3 个任务应被并发上限挡住），再全部放行
	waitFor(t, func() bool { return g.maxConcurrent() >= 2 })
	waitFor(t, func() bool { return g.maxConcurrent() < 3 }) // 上限 2 生效
	for _, vid := range []string{"a", "b", "c"} {
		g.open(vid)
	}
	for _, id := range []string{"hongguo:700110", "hongguo:700111", "hongguo:700112"} {
		waitStatus(t, e, id, store.JobDone)
	}
}

// 分集边界暂停：当前集完成后停住；恢复后继续剩余分集。
func TestJobRunnerPauseAtBoundaryAndResume(t *testing.T) {
	e, g, _ := gateEngine(t, 1)
	media := []byte("P")
	var hits int32
	msrv := newMediaServer(t, media, &hits, nil)
	g.fakeSource.meta = DramaMeta{Title: "暂停剧", Episodes: []EpisodeInfo{{1, "p1"}, {2, "p2"}}}
	g.fakeSource.streams = map[string]*Stream{
		"p1": {URL: msrv.URL + "/p1.mp4", DurationMS: 1000},
		"p2": {URL: msrv.URL + "/p2.mp4", DurationMS: 1000},
	}
	g.holdVID("p1")

	r := NewJobRunner(e)
	rctx, cancel := context.WithCancel(context.Background())
	r.Start(rctx)
	defer func() { cancel(); r.Stop() }()
	job120, err := r.AddJob(context.Background(), "700120", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	// 第 1 集取流被闸住 → 等到它真正开跑，再暂停，然后放行
	waitEpisode(t, e, "hongguo:700120", 1, store.EpDownloading)
	if err := r.PauseJob(job120.ID); err != nil {
		t.Fatal(err)
	}
	g.open("p1")
	waitStatus(t, e, "hongguo:700120", store.JobPaused)
	eps, _ := e.Store.Episodes(job120.ID)
	if eps[1].Status != store.EpPending {
		t.Fatalf("暂停后第 2 集应保持 pending: %+v", eps[1])
	}
	if err := r.ResumeJob(job120.ID); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, "hongguo:700120", store.JobDone)
}

// 删除运行中任务：分集边界发现任务已删即停，执行器继续可用。
func TestJobRunnerStopsDeletedJob(t *testing.T) {
	e, g, _ := gateEngine(t, 1)
	media := []byte("D")
	var hits int32
	msrv := newMediaServer(t, media, &hits, nil)
	g.seriesMeta["700130"] = DramaMeta{Title: "删除常驻剧A", Episodes: []EpisodeInfo{{1, "d1"}, {2, "d2"}}}
	g.seriesMeta["700131"] = DramaMeta{Title: "删除常驻剧B", Episodes: []EpisodeInfo{{1, "e1"}}}
	g.fakeSource.meta = DramaMeta{Title: "删除常驻剧", Episodes: []EpisodeInfo{{1, "d1"}, {2, "d2"}}}
	g.fakeSource.streams = map[string]*Stream{
		"d1": {URL: msrv.URL + "/d1.mp4", DurationMS: 1000},
		"d2": {URL: msrv.URL + "/d2.mp4", DurationMS: 1000},
		"e1": {URL: msrv.URL + "/e1.mp4", DurationMS: 1000},
	}
	g.holdVID("d1")

	r := NewJobRunner(e)
	rctx, cancel := context.WithCancel(context.Background())
	r.Start(rctx)
	defer func() { cancel(); r.Stop() }()
	if _, err := r.AddJob(context.Background(), "700130", nil, 0); err != nil {
		t.Fatal(err)
	}
	waitEpisode(t, e, "hongguo:700130", 1, store.EpDownloading)
	if err := r.DeleteJob("hongguo:700130", true); err != nil {
		t.Fatal(err)
	}
	g.open("d1")
	// 任务行已删；执行器存活（能继续处理新任务；复用同一剧集数据第 1 集）
	if _, err := r.AddJob(context.Background(), "700131", []int{1}, 0); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, "hongguo:700131", store.JobDone)
}

// 事件回调：任务状态/进度变化时被调用（SSE 的驱动源）。
func TestJobRunnerEmitsEvents(t *testing.T) {
	e, g, _ := gateEngine(t, 2)
	media := []byte("E")
	var hits int32
	msrv := newMediaServer(t, media, &hits, nil)
	g.fakeSource.meta = DramaMeta{Title: "事件剧", Episodes: []EpisodeInfo{{1, "v1"}}}
	g.fakeSource.streams = map[string]*Stream{"v1": {URL: msrv.URL + "/v1.mp4", DurationMS: 1000}}

	var events int32
	e.OnEvent = func() { atomic.AddInt32(&events, 1) }

	r := NewJobRunner(e)
	rctx, cancel := context.WithCancel(context.Background())
	r.Start(rctx)
	defer func() { cancel(); r.Stop() }()
	if _, err := r.AddJob(context.Background(), "700140", []int{1}, 0); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, "hongguo:700140", store.JobDone)
	if atomic.LoadInt32(&events) == 0 {
		t.Fatal("任务全流程没有任何事件回调")
	}
}

// 实时进度：下载中能查到当前集与字节数/速度；完成后清空。
func TestJobRunnerLiveProgress(t *testing.T) {
	e, g, _ := gateEngine(t, 1)
	media := []byte(fmt.Sprintf("%02048d", 0))
	var hits int32
	msrv := newMediaServer(t, media, &hits, nil)
	g.fakeSource.meta = DramaMeta{Title: "进度剧", Episodes: []EpisodeInfo{{1, "v1"}}}
	g.fakeSource.streams = map[string]*Stream{"v1": {URL: msrv.URL + "/v1.mp4", DurationMS: 1000}}
	g.holdVID("v1")

	r := NewJobRunner(e)
	rctx, cancel := context.WithCancel(context.Background())
	r.Start(rctx)
	defer func() { cancel(); r.Stop() }()
	if _, err := r.AddJob(context.Background(), "700150", []int{1}, 0); err != nil {
		t.Fatal(err)
	}
	waitEpisode(t, e, "hongguo:700150", 1, store.EpDownloading)
	live, ok := e.LiveJob(1)
	if !ok {
		t.Fatal("下载中应能查到实时进度")
	}
	if live.EpisodeIndex != 1 {
		t.Fatalf("当前集 = %d", live.EpisodeIndex)
	}
	g.open("v1")
	waitStatus(t, e, "hongguo:700150", store.JobDone)
	if _, ok := e.LiveJob(1); ok {
		t.Fatal("完成后实时进度应清空")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for !cond() {
		select {
		case <-deadline:
			t.Fatal("条件等待超时")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func waitEpisode(t *testing.T, e *Engine, dramaID string, idx int, want string) {
	t.Helper()
	j, err := e.Store.GetJob(dramaID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		eps, _ := e.Store.Episodes(j.ID)
		for _, ep := range eps {
			if ep.Index == idx && ep.Status == want {
				return
			}
		}
		select {
		case <-deadline:
			eps, _ := e.Store.Episodes(j.ID)
			t.Fatalf("分集 %d 未进入 %s: %+v", idx, want, eps)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// danmaku 相关编译期锚（gateSource 嵌 fakeSource 时保持接口完整）。
var _ Source = (*gateSource)(nil)
var _ = ass.Comment{}
