package pipeline

// 引擎热读取接缝（Engine.SettingsLookup）测试：
// 并发上限在下个任务领取时生效、ASS 开关在下个分集导出时生效。
// 设置页保存后无需重启进程（spec §4 设置闭环）。

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2017fighting/guo/internal/ass"
	"github.com/2017fighting/guo/internal/store"
)

// blockingSource 在 ResolveStream 处阻塞并计数在跑任务（每任务 1 集，
// 阻塞中的 resolve 数 = 并发领取的剧集任务数）。
type blockingSource struct {
	fakeSource
	start    chan struct{}
	inflight *int32
}

func (b *blockingSource) ResolveStream(ctx context.Context, seriesID, vid string, quality int) (*Stream, error) {
	atomic.AddInt32(b.inflight, 1)
	<-b.start
	return b.fakeSource.ResolveStream(ctx, seriesID, vid, quality)
}

func waitInflight(t *testing.T, n *int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(n) >= want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("在跑任务数未达到 %d（当前 %d）", want, atomic.LoadInt32(n))
}

func TestRunReadsHotConcurrencyAtClaim(t *testing.T) {
	e, src, _, _ := newEngine(t)
	var hits int32
	msrv := newMediaServer(t, []byte("FAKEVIDEO"), &hits, nil)
	src.meta = DramaMeta{Title: "剧", Episodes: []EpisodeInfo{{Index: 1, VID: "1"}}}
	src.streams = map[string]*Stream{"1": {URL: msrv.URL, DurationMS: 0}}

	var inflight int32
	block := &blockingSource{fakeSource: *src, start: make(chan struct{}), inflight: &inflight}
	e.Source = block

	var limit int32 = 1
	e.SettingsLookup = func() HotSettings {
		return HotSettings{Concurrency: int(atomic.LoadInt32(&limit)), ASSExport: false}
	}
	mustAdd(t, e, "700001", []int{1})
	mustAdd(t, e, "700002", []int{1})
	mustAdd(t, e, "700003", []int{1})

	done := make(chan error, 1)
	go func() { done <- e.Run(context.Background()) }()

	// 并发 1：只有第 1 个任务被领取
	waitInflight(t, &inflight, 1)
	time.Sleep(250 * time.Millisecond)
	if got := atomic.LoadInt32(&inflight); got != 1 {
		t.Fatalf("并发 1 时在跑任务应恰好 1 个，实际 %d", got)
	}
	// 上限热调到 3：无需任何任务结束，其余任务即被领取
	atomic.StoreInt32(&limit, 3)
	waitInflight(t, &inflight, 3)
	close(block.start)
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	jobs, err := e.Store.ListJobs()
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		if j.Status != store.JobDone {
			t.Errorf("任务 %s 状态 = %s, want done", j.DramaID, j.Status)
		}
	}
}

func TestRunShrunkConcurrencyDelaysNextClaim(t *testing.T) {
	e, src, _, _ := newEngine(t)
	var hits int32
	msrv := newMediaServer(t, []byte("FAKEVIDEO"), &hits, nil)
	src.meta = DramaMeta{Title: "剧", Episodes: []EpisodeInfo{{Index: 1, VID: "1"}}}
	src.streams = map[string]*Stream{"1": {URL: msrv.URL, DurationMS: 0}}

	var inflight int32
	block := &blockingSource{fakeSource: *src, start: make(chan struct{}), inflight: &inflight}
	e.Source = block

	var limit int32 = 3
	e.SettingsLookup = func() HotSettings {
		return HotSettings{Concurrency: int(atomic.LoadInt32(&limit)), ASSExport: false}
	}
	mustAdd(t, e, "700001", []int{1})
	mustAdd(t, e, "700002", []int{1})
	mustAdd(t, e, "700003", []int{1})

	done := make(chan error, 1)
	go func() { done <- e.Run(context.Background()) }()
	waitInflight(t, &inflight, 3)

	// 上限缩到 1：不抢占在跑任务，放行一个后不再补位
	atomic.StoreInt32(&limit, 1)
	close(block.start) // 3 个 resolve 全部放行
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	// 全部自然结束；此用例主要回归「缩容不 panic/不死锁」
	jobs, err := e.Store.ListJobs()
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		if j.Status != store.JobDone {
			t.Errorf("任务 %s 状态 = %s, want done", j.DramaID, j.Status)
		}
	}
}

func TestASSExportReadsHotSettings(t *testing.T) {
	e, src, _, root := newEngine(t)
	var hits int32
	msrv := newMediaServer(t, []byte("FAKEVIDEO"), &hits, nil)
	src.streams = map[string]*Stream{"1": {URL: msrv.URL, DurationMS: 60000}}
	src.danmaku = map[string][]ass.Comment{"1": {{ID: "c1", Text: "前排", TimeMS: 500}}}

	current := HotSettings{Concurrency: 2, ASSExport: false}
	e.SettingsLookup = func() HotSettings { return current }
	e.ASSExport = true // 静态字段保持开——热读取应覆盖它

	src.meta = DramaMeta{Title: "关弹幕剧", Episodes: []EpisodeInfo{{Index: 1, VID: "1"}}}
	mustAdd(t, e, "700001", []int{1})
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(root, "关弹幕剧", "Season 01", "S01E001.zh.ass")) {
		t.Fatal("热读取为关时不应导出 ASS")
	}

	src.meta = DramaMeta{Title: "开弹幕剧", Episodes: []EpisodeInfo{{Index: 1, VID: "1"}}}
	mustAdd(t, e, "700002", []int{1})
	current.ASSExport = true // 保存后生效：影响后续分集导出
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !fileExists(filepath.Join(root, "开弹幕剧", "Season 01", "S01E001.zh.ass")) {
		t.Fatal("热读取为开时应导出 ASS")
	}
}

func TestHotSettingsGetters(t *testing.T) {
	// 无接缝：静态字段 + 缺省
	e := &Engine{}
	if e.concurrency() != 2 {
		t.Errorf("默认并发 = %d, want 2", e.concurrency())
	}
	e.Concurrency = 4
	if e.concurrency() != 4 {
		t.Errorf("静态并发 = %d, want 4", e.concurrency())
	}
	if !e.assExport() { // 零值 false：由 cmd 启动时负责给默认
	}
	// 接缝：热值优先；越界钳回 1–6；0 视为未提供回退静态值
	e.SettingsLookup = func() HotSettings { return HotSettings{Concurrency: 5, ASSExport: true} }
	if e.concurrency() != 5 || !e.assExport() {
		t.Errorf("热读取应覆盖静态值: %d/%v", e.concurrency(), e.assExport())
	}
	e.SettingsLookup = func() HotSettings { return HotSettings{Concurrency: 99, ASSExport: false} }
	if e.concurrency() != 6 {
		t.Errorf("热并发 99 应钳到 6, got %d", e.concurrency())
	}
	e.SettingsLookup = func() HotSettings { return HotSettings{Concurrency: 0, ASSExport: false} }
	if e.concurrency() != 4 {
		t.Errorf("热并发 0 应回退静态值 4, got %d", e.concurrency())
	}
}
