package pipeline

// 进度跟踪：常驻模式下引擎对外暴露「当前在跑什么、跑到多少字节、速度多少」，
// 以及队列变化事件回调（SSE 的驱动源）。CLI 一次性 Run 不设置 OnEvent 也能正常工作。

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/2017fighting/guo/internal/layout"
	"github.com/2017fighting/guo/internal/store"
)

// LiveInfo 单个在跑任务的实时进度快照。
type LiveInfo struct {
	JobID        int64
	EpisodeIndex int     // 正在处理的分集（1 起）
	Downloaded   int64   // 本集已落盘字节（.part 续传基线 + 本次新增）
	Speed        float64 // 近端窗口字节速率（bytes/s）
	At           time.Time
}

// progressTracker 引擎内共享的进度/事件状态（零值可用）。
type progressTracker struct {
	mu     sync.Mutex
	live   map[int64]LiveInfo
	jfDone map[int64]bool // Jellyfin 已刷新（按任务；进程内记忆，重启即失）
}

func (p *progressTracker) setLive(info LiveInfo) {
	p.mu.Lock()
	if p.live == nil {
		p.live = map[int64]LiveInfo{}
	}
	p.live[info.JobID] = info
	p.mu.Unlock()
}

func (p *progressTracker) liveJob(jobID int64) (LiveInfo, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	info, ok := p.live[jobID]
	return info, ok
}

func (p *progressTracker) clearLive(jobID int64) {
	p.mu.Lock()
	delete(p.live, jobID)
	p.mu.Unlock()
}

func (p *progressTracker) markJellyfin(jobID int64) {
	p.mu.Lock()
	if p.jfDone == nil {
		p.jfDone = map[int64]bool{}
	}
	p.jfDone[jobID] = true
	p.mu.Unlock()
}

func (p *progressTracker) jellyfinDone(jobID int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.jfDone[jobID]
}

// OnEvent 队列可见变化（任务/分集状态跃迁、进度心跳）时被调用。
// 常驻模式接 SSE 广播；为 nil 时静默。回调里不要做重活（在引擎锁外调用）。
func (e *Engine) emitEvent() {
	if e.OnEvent != nil {
		e.OnEvent()
	}
}

// LiveJob 查询单个任务的实时进度；不在跑时 ok=false。
func (e *Engine) LiveJob(jobID int64) (LiveInfo, bool) {
	return e.progress.liveJob(jobID)
}

// JellyfinRefreshed 该任务的整剧 Jellyfin 刷新是否已触发（进程内记忆）。
func (e *Engine) JellyfinRefreshed(jobID int64) bool {
	return e.progress.jellyfinDone(jobID)
}

// JobBytes 汇总任务字节数：downloaded = 已完成集成品 + 当前集 .part/实时进度；
// estimated = 按已完成集平均大小外推的整剧估值（无已完成集时 0 = 未知）。
func (e *Engine) JobBytes(job *store.Job) (downloaded, estimated int64) {
	eps, err := e.Store.Episodes(job.ID)
	if err != nil {
		return 0, 0
	}
	season := layout.SeasonDir(layout.ShowDir(e.MediaRoot, job.Title, job.Year), 1)
	var doneBytes int64
	doneCount := 0
	for _, ep := range eps {
		if ep.Status != store.EpDone {
			continue
		}
		if fi, err := os.Stat(filepath.Join(season, layout.EpisodeStem(1, ep.Index)+".mp4")); err == nil {
			doneBytes += fi.Size()
		}
		doneCount++
	}
	downloaded = doneBytes
	if live, ok := e.LiveJob(job.ID); ok {
		if part, err := os.Stat(filepath.Join(season, layout.EpisodeStem(1, live.EpisodeIndex)+".part")); err == nil && part.Size() > live.Downloaded {
			downloaded += part.Size() // .part 落盘为准（含续传基线）
		} else {
			downloaded += live.Downloaded
		}
	}
	if doneCount > 0 {
		estimated = doneBytes / int64(doneCount) * int64(len(eps))
		if live, ok := e.LiveJob(job.ID); ok {
			estimated += live.Downloaded // 当前集未计入 doneBytes
		}
	}
	return downloaded, estimated
}
