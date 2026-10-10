package pipeline

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/2017fighting/guo/internal/store"
)

// JobRunner：把一次性 Run 常驻化的事件驱动执行器。
// 唤醒源 = 任务创建/恢复/重试（经由本类型的包装方法）+ 启动 + 槽位释放；
// 无可跑任务时阻塞在 wake 通道上，静默不轮询。并发语义与 Engine.Run 一致：
// 全局并发 N 部剧（默认 2）、剧内分集串行、暂停在分集边界生效、
// .part 字节续传、403/410 重取、整剧完成触发一次 Jellyfin 刷新。
//
// 启动时把上次进程中断留下的 running 任务复位为 queued（分集 downloading/
// merging 回 pending），配合 .part 续传实现断电重启不重拉。
type JobRunner struct {
	engine  *Engine
	wake    chan struct{}
	freed   chan struct{} // 一个任务 goroutine 结束（腾出槽位）
	mu      sync.Mutex
	running map[int64]bool
	wg      sync.WaitGroup

	stopOnce sync.Once
}

// NewJobRunner 包装引擎；Start 前可安全调用各包装方法（仅多唤醒一次空转）。
func NewJobRunner(e *Engine) *JobRunner {
	return &JobRunner{
		engine:  e,
		wake:    make(chan struct{}, 1),
		freed:   make(chan struct{}, 1),
		running: map[int64]bool{},
	}
}

// Start 启动常驻循环（幂等）。ctx 取消后等待在跑任务收尾再返回（Stop 语义）。
func (r *JobRunner) Start(ctx context.Context) {
	r.stopOnce.Do(func() {
		// 断电重启恢复：running→queued、卡住分集→pending（.part 续传不重拉）
		if err := r.resetStuckJobs(); err != nil {
			r.engine.log(fmt.Sprintf("启动复位遗留任务失败: %v", err))
		}
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			r.loop(ctx)
		}()
	})
}

// Stop 等待循环与在跑任务收尾（ctx 已取消时任务在分集边界退出）。
func (r *JobRunner) Stop() {
	r.wg.Wait()
}

// Notify 非阻塞唤醒一次调度（外部直接改库时用；包装方法已内置）。
func (r *JobRunner) Notify() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *JobRunner) loop(ctx context.Context) {
	for {
		r.dispatch(ctx)
		select {
		case <-ctx.Done():
			r.drain(ctx)
			return
		case <-r.wake:
		case <-r.freed:
		}
	}
}

// drain 等 in-flight 任务 goroutine 全部退出（ctx 取消后它们会在
// 分集边界/下载中断点返回）。
func (r *JobRunner) drain(ctx context.Context) {
	for {
		r.mu.Lock()
		n := len(r.running)
		r.mu.Unlock()
		if n == 0 {
			return
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-r.freed:
		}
	}
}

// dispatch 扫描队列，把 queued 任务拉起到全局并发上限。
// failed 不自动重跑（等待用户「重试失败集」或恢复操作置回 queued），
// 避免常驻循环对终态失败任务反复空转。
func (r *JobRunner) dispatch(ctx context.Context) {
	jobs, err := r.engine.Store.ListJobs()
	if err != nil {
		r.engine.log(fmt.Sprintf("调度扫描失败: %v", err))
		return
	}
	for i := range jobs {
		j := jobs[i]
		if j.Status != store.JobQueued {
			continue
		}
		r.mu.Lock()
		busy := r.running[j.ID]
		n := len(r.running)
		r.mu.Unlock()
		if busy || n >= r.engine.concurrency() {
			continue
		}
		r.launch(ctx, j.ID)
	}
}

func (r *JobRunner) launch(ctx context.Context, jobID int64) {
	r.mu.Lock()
	if r.running[jobID] || len(r.running) >= r.engine.concurrency() {
		r.mu.Unlock()
		return
	}
	r.running[jobID] = true
	r.mu.Unlock()
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer func() {
			r.mu.Lock()
			delete(r.running, jobID)
			r.mu.Unlock()
			select { // 通知循环：槽位释放，可能有排队任务
			case r.freed <- struct{}{}:
			default:
			}
		}()
		if err := r.engine.runJob(ctx, jobID); err != nil && ctx.Err() == nil {
			r.engine.log(fmt.Sprintf("任务 %d 失败: %v", jobID, err))
		}
	}()
}

// resetStuckJobs 上次进程崩溃现场恢复：running 任务 → queued，
// 其 downloading/merging 分集 → pending（成品 done 分集不动）。
func (r *JobRunner) resetStuckJobs() error {
	jobs, err := r.engine.Store.ListJobs()
	if err != nil {
		return err
	}
	for i := range jobs {
		j := jobs[i]
		if j.Status != store.JobRunning {
			continue
		}
		if err := r.engine.Store.SetJobStatus(j.ID, store.JobQueued); err != nil {
			return err
		}
		eps, err := r.engine.Store.Episodes(j.ID)
		if err != nil {
			return err
		}
		for _, ep := range eps {
			if ep.Status == store.EpDownloading || ep.Status == store.EpMerging {
				if err := r.engine.Store.SetEpisodeStatus(j.ID, ep.Index, store.EpPending); err != nil {
					return err
				}
			}
		}
		r.engine.log(fmt.Sprintf("任务 %d 复位为 queued（上次进程中断，.part 续传）", j.ID))
	}
	return nil
}

// ---- 引擎变更入口的常驻包装：改库后唤醒调度 ----

// AddJob 建任务并唤醒调度（幂等语义同 Engine.AddJob）。
func (r *JobRunner) AddJob(ctx context.Context, seriesID string, indexes []int, quality int) (*store.Job, error) {
	job, err := r.engine.AddJob(ctx, seriesID, indexes, quality)
	r.Notify()
	return job, err
}

// PauseJob 暂停（分集边界生效）。
func (r *JobRunner) PauseJob(jobID int64) error {
	err := r.engine.PauseJob(jobID)
	r.Notify()
	return err
}

// ResumeJob 恢复到队列。
func (r *JobRunner) ResumeJob(jobID int64) error {
	err := r.engine.ResumeJob(jobID)
	r.Notify()
	return err
}

// RetryJob 重置失败分集并重新入队。
func (r *JobRunner) RetryJob(jobID int64) error {
	err := r.engine.RetryJob(jobID)
	r.Notify()
	return err
}

// DeleteJob 删除任务（可选保留视频）；运行中的任务在分集边界发现后自行停止。
func (r *JobRunner) DeleteJob(dramaID string, keepVideo bool) error {
	err := r.engine.DeleteJob(dramaID, keepVideo)
	r.Notify()
	return err
}

// Job 按 ID 查任务（server 路由用）。
func (r *JobRunner) Job(jobID int64) (*store.Job, error) {
	return r.engine.jobByID(jobID)
}
