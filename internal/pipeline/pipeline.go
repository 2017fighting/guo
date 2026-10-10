// Package pipeline 是下载管线引擎：任务=剧+选集（#7 决议），
// 全局并发 N 部剧、剧内分集串行、MP4 直链按字节断点续传（分段续传的
// 直链等价物——红果为 CENC MP4 单文件直链，见 hongguo-protocol.md §6）、
// ffmpeg demux 层解密合并（-decryption_key，无转码）、每集落
// NFO + zh.ass、整剧完成触发 Jellyfin 刷新。
package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/2017fighting/guo/internal/ass"
	"github.com/2017fighting/guo/internal/jellyfin"
	"github.com/2017fighting/guo/internal/layout"
	"github.com/2017fighting/guo/internal/nfo"
	"github.com/2017fighting/guo/internal/store"
)

// DramaMeta 剧级元数据（红果详情卡片字段子集）。
type DramaMeta struct {
	SeriesID  string
	Title     string
	Year      string
	Plot      string
	Genres    []string
	CoverURL  string
	FanartURL string
	Episodes  []EpisodeInfo // 按 index 升序
}

// EpisodeInfo 分集：序号（1 起）+ 红果 videoID。
type EpisodeInfo struct {
	Index int
	VID   string
}

// Stream 一次取流结果（三级回退后的胜者）。
type Stream struct {
	URL        string
	Referer    string // Web 线路带 hongguoduanju.com/，App/兜底线路不带
	CENCKeyHex string // 空 = 无加密
	Quality    int    // 画质打分（高优），选档用
	Definition string
	DurationMS int64
}

// Source 抽象站源（红果实现的缝）。
type Source interface {
	Detail(ctx context.Context, seriesID string) (*DramaMeta, error)
	// ResolveStream 解析分集可下载流；quality 为期望档（0 = 最高）。
	ResolveStream(ctx context.Context, seriesID, vid string, quality int) (*Stream, error)
	// DanmakuAll 拉完整集弹幕（源负责窗口分页），durationMS 为本集真实时长。
	DanmakuAll(ctx context.Context, seriesID, vid string, durationMS int64) ([]ass.Comment, error)
}

// Runner 执行外部 ffmpeg（测试用假实现替换）。
type Runner interface {
	Run(ctx context.Context, args ...string) error
}

// ExecRunner 真实 ffmpeg 执行器（容器/宿主机内置 ffmpeg 二进制）。
type ExecRunner struct {
	Path string // 默认 ffmpeg
}

func (r *ExecRunner) Run(ctx context.Context, args ...string) error {
	path := r.Path
	if path == "" {
		path = "ffmpeg"
	}
	cmd := exec.CommandContext(ctx, path, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(stderr.String()), err)
	}
	return nil
}

// Engine 下载引擎。
type Engine struct {
	Store       *store.Store
	Source      Source
	MediaRoot   string
	FFMpeg      Runner
	Concurrency int  // 全局同时下载数，默认 2
	ASSExport   bool // 弹幕导出默认开
	MaxRetries  int  // 分集自动重试上限，默认 3
	Jellyfin    *jellyfin.Client
	HTTP        *http.Client
	Log         func(string)

	// OnEvent 队列可见变化（状态跃迁/进度心跳）回调；常驻模式接 SSE，nil=静默。
	OnEvent func()
	// progress 实时进度与 Jellyfin 刷新标记（零值可用）。
	progress progressTracker

	// SettingsLookup 非空时引擎热读取设置：每次领取任务前读并发上限、
	// 每个分集导出前读 ASS 开关（设置页保存后即时生效，不重启进程）。
	// 为空则退回静态字段（CLI 一次性启动口径）。
	SettingsLookup func() HotSettings
}

// HotSettings 引擎热读取的设置子集（cmd/guo serve 从 settings 表供值）。
type HotSettings struct {
	Concurrency int
	ASSExport   bool
}

// IPhoneUA 媒体与页面请求共用 UA（对齐 guoapp mediaRequestHeaders）。
const IPhoneUA = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"

const metaKeyCENC = "cenc_key"

// metaKeyError 分集最近一次失败原因（episode_meta，供队列页展示出路）。
const metaKeyError = "last_error"

// truncateErr 失败原因入库截断（人话展示用，不存堆栈长文）。
func truncateErr(msg string) string {
	const limit = 300
	runes := []rune(msg)
	if len(runes) <= limit {
		return msg
	}
	return string(runes[:limit]) + "…"
}

// maxConcurrency 全局并发上限（#7 决议：可配 1–6）。
const maxConcurrency = 6

func (e *Engine) concurrency() int {
	if e.SettingsLookup != nil {
		if n := e.SettingsLookup().Concurrency; n > 0 {
			return clampConcurrency(n)
		}
	}
	return clampConcurrency(e.Concurrency) // <=0 时钳为默认 2
}
func (e *Engine) assExport() bool {
	if e.SettingsLookup != nil {
		return e.SettingsLookup().ASSExport
	}
	return e.ASSExport
}

// clampConcurrency 钳到 1–6（#7 决议；<=0 视为未配置取默认 2）。
func clampConcurrency(n int) int {
	if n <= 0 {
		return 2
	}
	if n > maxConcurrency {
		return maxConcurrency
	}
	return n
}
func (e *Engine) maxRetries() int {
	if e.MaxRetries <= 0 {
		return 3
	}
	return e.MaxRetries
}
func (e *Engine) log(msg string) {
	if e.Log != nil {
		e.Log(msg)
	}
}
func (e *Engine) client() *http.Client {
	if e.HTTP != nil {
		return e.HTTP
	}
	return http.DefaultClient
}

// AddJob 建任务：入库 + 立即写剧集级产物（tvshow.nfo / poster.jpg），
// 使 Jellyfin 在下载过程中即可见该剧（#7 决议第 1 项）。
// quality 为期望画质档（0 = 最高）；已完结任务补新分集时自动回到队列（「更新本剧」）。
func (e *Engine) AddJob(ctx context.Context, seriesID string, indexes []int, quality int) (*store.Job, error) {
	meta, err := e.Source.Detail(ctx, seriesID)
	if err != nil {
		return nil, fmt.Errorf("detail: %w", err)
	}
	eps := make(map[int]string, len(meta.Episodes))
	for _, ep := range meta.Episodes {
		if len(indexes) == 0 { // 空 = 整剧
			eps[ep.Index] = ep.VID
			continue
		}
		for _, want := range indexes {
			if ep.Index == want {
				eps[ep.Index] = ep.VID
			}
		}
	}
	if len(eps) == 0 {
		return nil, errors.New("no episodes selected")
	}
	job, err := e.Store.CreateJob("hongguo:"+seriesID, meta.Title, meta.Year, quality, eps)
	if err != nil {
		return nil, err
	}
	// 「更新本剧」：已完结任务补了新分集时重新入队
	if job.Status == store.JobDone {
		if eps2, lerr := e.Store.Episodes(job.ID); lerr == nil {
			for _, ep2 := range eps2 {
				if ep2.Status != store.EpDone {
					_ = e.Store.SetJobStatus(job.ID, store.JobQueued)
					job.Status = store.JobQueued
					break
				}
			}
		}
	}

	showDir := layout.ShowDir(e.MediaRoot, meta.Title, meta.Year)
	if err := os.MkdirAll(filepath.Join(showDir, "Season 01"), 0o755); err != nil {
		return nil, err
	}
	showNFO, err := nfo.Show{
		Title: meta.Title, Plot: meta.Plot, Year: meta.Year,
		Genres: meta.Genres, UniqueID: seriesID,
	}.Marshal()
	if err != nil {
		return nil, err
	}
	if err := atomicWrite(layout.Show{Dir: showDir}.NFO(), showNFO); err != nil {
		return nil, err
	}
	if err := e.fetchImage(ctx, meta.CoverURL, layout.Show{Dir: showDir}.Poster()); err != nil {
		e.log(fmt.Sprintf("poster 跳过: %v", err))
	}
	if meta.FanartURL != "" {
		if err := e.fetchImage(ctx, meta.FanartURL, layout.Show{Dir: showDir}.Fanart()); err != nil {
			e.log(fmt.Sprintf("fanart 跳过: %v", err))
		}
	}
	e.emitEvent()
	return job, nil
}

// Run 排空一次队列：并发拉起至多 Concurrency 个可运行任务，全部终态后返回。
// Run 排空一次队列：并发拉起任务，全部终态后返回。
// 每次领取任务前热读取并发上限（SettingsLookup 非空时设置页保存后
// 下一次领取即生效；改动不抢占进行中的分集，与暂停同理）。
func (e *Engine) Run(ctx context.Context) error {
	jobs, err := e.Store.ListJobs()
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	released := make(chan struct{}, maxConcurrency) // 任务结束投递额度信号
	inflight := 0                                   // 仅主循环读写
	for i := range jobs {
		j := jobs[i]
		if j.Status != store.JobQueued && j.Status != store.JobFailed {
			continue
		}
		for inflight >= e.concurrency() {
			select {
			case <-released:
				inflight--
			case <-time.After(200 * time.Millisecond):
				// 等待期间上限可能被调大，重读一次再试
			case <-ctx.Done():
				wg.Wait()
				return ctx.Err()
			}
		}
		inflight++
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { released <- struct{}{} }()
			if err := e.runJob(ctx, j.ID); err != nil {
				e.log(fmt.Sprintf("任务 %d 失败: %v", j.ID, err))
			}
		}()
	}
	wg.Wait()
	return nil
}

func (e *Engine) runJob(ctx context.Context, jobID int64) error {
	job, err := e.jobByID(jobID)
	if err != nil {
		return err
	}
	if err := e.Store.SetJobStatus(jobID, store.JobRunning); err != nil {
		return err
	}
	e.emitEvent()
	defer e.clearLive(jobID)
	eps, err := e.Store.Episodes(jobID)
	if err != nil {
		return err
	}
	anyFailed := false
	for _, ep := range eps {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// 暂停即停（分集边界）；任务被删也停
		fresh, _ := e.jobByID(jobID)
		if fresh == nil {
			e.log(fmt.Sprintf("任务 %d 已删除，停止处理", jobID))
			e.clearLive(jobID)
			return nil
		}
		if fresh.Status == store.JobPaused {
			e.log(fmt.Sprintf("任务 %d 已暂停", jobID))
			e.clearLive(jobID)
			e.emitEvent()
			return nil
		}
		if ep.Status == store.EpDone {
			continue
		}
		if ep.Status == store.EpFailed && ep.Retries >= e.maxRetries() {
			anyFailed = true // 永久失败，不再尝试
			continue
		}
		// 分集失败自动重试有限次（#7 决议第 5 项）；重试耗尽即放下，不阻塞后续分集
		epDone := false
	forAttempt:
		for attempt := 0; ; attempt++ {
			if perr := e.processEpisode(ctx, job, ep.Index, ep.VID); perr == nil {
				epDone = true
				break forAttempt
			} else {
				e.log(fmt.Sprintf("分集 %d 第 %d 次尝试失败: %v", ep.Index, attempt+1, perr))
				if errors.Is(perr, store.ErrNotFound) {
					// 任务已被删除：立即收手，不占用重试退避
					e.clearLive(jobID)
					return nil
				}
				_ = e.Store.SaveEpisodeMeta(jobID, ep.Index, metaKeyError, truncateErr(perr.Error()))
				_ = e.Store.SetEpisodeStatus(jobID, ep.Index, store.EpFailed)
				if latest, lerr := e.Store.Episodes(jobID); lerr == nil {
					for _, fe := range latest {
						if fe.Index == ep.Index && fe.Retries >= e.maxRetries() {
							break forAttempt
						}
					}
				}
				select {
				case <-time.After(time.Duration(attempt+1) * 2 * time.Second):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
		if !epDone {
			anyFailed = true
		}
	}
	if anyFailed {
		if err := e.Store.SetJobStatus(jobID, store.JobFailed); err != nil {
			return err
		}
		e.clearLive(jobID)
		e.emitEvent()
		return nil
	}
	if err := e.Store.SetJobStatus(jobID, store.JobDone); err != nil {
		return err
	}
	e.clearLive(jobID)
	e.emitEvent()
	if e.Jellyfin != nil {
		e.Jellyfin.RefreshAsync() // 整剧完成触发一次（#7 决议）
		e.progress.markJellyfin(jobID)
	}
	return nil
}

func (e *Engine) jobByID(id int64) (*store.Job, error) {
	jobs, err := e.Store.ListJobs()
	if err != nil {
		return nil, err
	}
	for i := range jobs {
		if jobs[i].ID == id {
			return &jobs[i], nil
		}
	}
	return nil, store.ErrNotFound
}

// processEpisode 单集全链路：取流 → 断点下载（密文）→ ffmpeg 解密合并 → NFO/ASS。
func (e *Engine) processEpisode(ctx context.Context, job *store.Job, index int, vid string) error {
	seriesID := strings.TrimPrefix(job.DramaID, "hongguo:")
	epLayout := layout.Episode{Dir: filepath.Join(layout.SeasonDir(
		layout.ShowDir(e.MediaRoot, job.Title, job.Year), 1)), Stem: layout.EpisodeStem(1, index)}
	if err := os.MkdirAll(epLayout.Dir, 0o755); err != nil {
		return err
	}
	if err := e.Store.SetEpisodeStatus(job.ID, index, store.EpDownloading); err != nil {
		return err
	}
	e.setLive(job.ID, index, 0, 0)
	e.emitEvent()

	stream, err := e.Source.ResolveStream(ctx, seriesID, vid, job.Quality)
	if err != nil {
		return fmt.Errorf("resolve: %w", err)
	}
	if stream.CENCKeyHex != "" {
		if err := e.Store.SaveEpisodeMeta(job.ID, index, metaKeyCENC, stream.CENCKeyHex); err != nil {
			return err
		}
	}

	// 断点下载：403/410（URL 过期）时重取流继续；返回生效的流（密钥可能已更新）。
	stream, err = e.download(ctx, stream, epLayout.Part(), job.ID, index, seriesID, vid, job.Quality)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}

	if err := e.Store.SetEpisodeStatus(job.ID, index, store.EpMerging); err != nil {
		return err
	}
	e.emitEvent()
	args := ffmpegArgs(stream.CENCKeyHex, epLayout.Part(), epLayout.Video())
	if err := e.FFMpeg.Run(ctx, args...); err != nil {
		return fmt.Errorf("ffmpeg: %w", err)
	}
	os.Remove(epLayout.Part())

	epNFO, err := nfo.Episode{
		Title: fmt.Sprintf("第 %d 集", index), ShowTitle: layout.ShowName(job.Title, job.Year),
		Season: 1, Episode: index, UniqueID: vid,
	}.Marshal()
	if err != nil {
		return err
	}
	if err := atomicWrite(epLayout.NFO(), epNFO); err != nil {
		return err
	}

	if e.assExport() && stream.DurationMS > 0 {
		if comments, derr := e.Source.DanmakuAll(ctx, seriesID, vid, stream.DurationMS); derr == nil && len(comments) > 0 {
			if err := atomicWrite(epLayout.ASS(), ass.Convert(comments, ass.Options{})); err != nil {
				e.log(fmt.Sprintf("分集 %d 弹幕导出失败: %v", index, err))
			}
		} else if derr != nil {
			e.log(fmt.Sprintf("分集 %d 弹幕拉取跳过: %v", index, derr))
		}
	}
	if err := e.Store.SetEpisodeStatus(job.ID, index, store.EpDone); err != nil {
		return err
	}
	e.emitEvent()
	return nil
}

// setLive 更新任务实时进度（下载心跳处节流调用）。
func (e *Engine) setLive(jobID int64, index int, downloaded int64, speed float64) {
	e.progress.setLive(LiveInfo{
		JobID: jobID, EpisodeIndex: index, Downloaded: downloaded, Speed: speed, At: time.Now(),
	})
}

func (e *Engine) clearLive(jobID int64) {
	e.progress.clearLive(jobID)
}

// ffmpegArgs 构造解密合并命令（docs/research/hongguo-protocol.md §6.5）：
// -decryption_key 是输入选项须在 -i 前；-c copy 不重编码，
// mov demuxer 解样本时解密，remux 产物即明文。
func ffmpegArgs(keyHex, in, out string) []string {
	args := []string{"-y"}
	if keyHex != "" {
		args = append(args, "-decryption_key", keyHex)
	}
	args = append(args,
		"-i", in,
		"-map", "0:v:0", "-map", "0:a:0?",
		"-c", "copy",
		"-movflags", "+faststart",
		out,
	)
	return args
}

// download 带 Range 断点续传的直链下载；403/410 时重取流地址一次。
// 返回最终生效的流（重取后地址/密钥可能已变，调用方必须用返回值）。
func (e *Engine) download(ctx context.Context, stream *Stream, partPath string,
	jobID int64, index int, seriesID, vid string, quality int) (*Stream, error) {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		err = e.downloadOnce(ctx, stream, partPath, jobID, index)
		if err == nil {
			return stream, nil
		}
		if !errors.Is(err, errURLExpired) {
			return nil, err
		}
		e.log(fmt.Sprintf("分集 %d 流地址过期，重取", index))
		fresh, rerr := e.Source.ResolveStream(ctx, seriesID, vid, quality)
		if rerr != nil {
			return nil, fmt.Errorf("re-resolve: %w", rerr)
		}
		if fresh.CENCKeyHex != "" {
			_ = e.Store.SaveEpisodeMeta(jobID, index, metaKeyCENC, fresh.CENCKeyHex)
		}
		stream = fresh
	}
	return nil, err
}

var errURLExpired = errors.New("stream url expired (403/410)")

func (e *Engine) downloadOnce(ctx context.Context, stream *Stream, partPath string, jobID int64, index int) error {
	f, err := os.OpenFile(partPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	offset, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, stream.URL, nil)
	if err != nil {
		return err
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	if stream.Referer != "" {
		req.Header.Set("Referer", stream.Referer)
	}
	req.Header.Set("User-Agent", IPhoneUA)
	req.Header.Set("Accept", "*/*")
	resp, err := e.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusForbidden, http.StatusGone:
		return errURLExpired
	case http.StatusPartialContent: // 206 续传
	case http.StatusOK: // 200 = 服务端忽略 Range，从头覆盖写
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if err := f.Truncate(0); err != nil {
			return err
		}
		offset = 0
	default:
		return fmt.Errorf("media status %d", resp.StatusCode)
	}
	// 分块拷贝 + 每 500ms 心跳：更新实时进度（速度近端窗口）并触发队列事件
	const heartbeat = 500 * time.Millisecond
	buf := make([]byte, 64<<10)
	var total int64 // 本次新增字节（不含续传基线）
	lastBeat := time.Now()
	lastBytes := offset
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			total += int64(n)
		}
		now := time.Now()
		if now.Sub(lastBeat) >= heartbeat {
			dt := now.Sub(lastBeat).Seconds()
			if dt > 0 {
				e.setLive(jobID, index, offset+total, float64(offset+total-lastBytes)/dt)
			}
			e.emitEvent()
			lastBeat, lastBytes = now, offset+total
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

func (e *Engine) fetchImage(ctx context.Context, url, dst string) error {
	if url == "" {
		return errors.New("empty image url")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := e.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("image status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	return atomicWrite(dst, data)
}

func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// PauseJob / ResumeJob / RetryJob / DeleteJob 任务控制（变更即发队列事件）。
func (e *Engine) PauseJob(jobID int64) error {
	if err := e.Store.SetJobStatus(jobID, store.JobPaused); err != nil {
		return err
	}
	e.emitEvent()
	return nil
}

func (e *Engine) ResumeJob(jobID int64) error {
	if err := e.Store.SetJobStatus(jobID, store.JobQueued); err != nil {
		return err
	}
	e.emitEvent()
	return nil
}

func (e *Engine) RetryJob(jobID int64) error {
	eps, err := e.Store.Episodes(jobID)
	if err != nil {
		return err
	}
	for _, ep := range eps {
		if ep.Status == store.EpFailed {
			if err := e.Store.SetEpisodeStatus(jobID, ep.Index, store.EpPending); err != nil {
				return err
			}
		}
	}
	if err := e.Store.SetJobStatus(jobID, store.JobQueued); err != nil {
		return err
	}
	e.emitEvent()
	return nil
}

// DeleteJob 删除任务；keepVideo=true 时保留已合并的视频与全部产物。
func (e *Engine) DeleteJob(dramaID string, keepVideo bool) error {
	job, err := e.Store.GetJob(dramaID)
	if err != nil {
		return err
	}
	if !keepVideo {
		showDir := layout.ShowDir(e.MediaRoot, job.Title, job.Year)
		if err := os.RemoveAll(showDir); err != nil {
			return err
		}
	}
	if err := e.Store.DeleteJob(dramaID); err != nil {
		return err
	}
	e.clearLive(job.ID)
	e.emitEvent()
	return nil
}
