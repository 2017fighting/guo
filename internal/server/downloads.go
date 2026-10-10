package server

// /api/v1/downloads：下载队列 CRUD + 控制。聚合卡片 = 剧卡（状态/进度/速度/
// 当前分集），数据 = store 持久状态 + 引擎实时进度（进程内）。
// 幂等：同剧重复 POST 返回既有任务；done 任务补集自动重入队（引擎语义）。

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/2017fighting/guo/internal/store"
)

type downloadCard struct {
	ID                 int64     `json:"id"`
	DramaID            string    `json:"drama_id"`
	SeriesID           string    `json:"series_id"`
	Title              string    `json:"title"`
	Year               string    `json:"year"`
	Quality            int       `json:"quality"`
	Status             string    `json:"status"`
	TotalEpisodes      int       `json:"total_episodes"`
	DoneEpisodes       int       `json:"done_episodes"`
	FailedEpisodes     int       `json:"failed_episodes"`
	CurrentEpisode     int       `json:"current_episode"` // 0 = 空闲
	DownloadedBytes    int64     `json:"downloaded_bytes"`
	EstimatedTotalByte int64     `json:"estimated_total_bytes"` // 0 = 未知（前端隐藏比例）
	SpeedBps           float64   `json:"speed_bps"`             // 0 = 空闲
	JellyfinRefreshed  bool      `json:"jellyfin_refreshed"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type episodeCard struct {
	Index     int       `json:"index"`
	VID       string    `json:"vid"`
	Status    string    `json:"status"`
	Retries   int       `json:"retries"`
	Error     string    `json:"error"` // 最近一次失败原因（episode_meta last_error）
	UpdatedAt time.Time `json:"updated_at"`
}

type queueSnapshot struct {
	Jobs []downloadCard `json:"jobs"`
}

// requireDownloads 未接线时统一 503。
func (s *Server) requireDownloads(w http.ResponseWriter) bool {
	if s.Downloads == nil {
		writeError(w, http.StatusServiceUnavailable, "下载服务未配置", "请检查服务启动日志")
		return false
	}
	return true
}

// jobCard 单任务聚合卡片。
func (s *Server) jobCard(job *store.Job) downloadCard {
	eps, _ := s.Downloads.Store().Episodes(job.ID)
	card := downloadCard{
		ID: job.ID, DramaID: job.DramaID, SeriesID: strings.TrimPrefix(job.DramaID, "hongguo:"),
		Title: job.Title, Year: job.Year, Quality: job.Quality, Status: job.Status,
		TotalEpisodes:     len(eps),
		JellyfinRefreshed: s.Downloads.JellyfinRefreshed(job.ID),
		CreatedAt:         job.CreatedAt, UpdatedAt: job.UpdatedAt,
	}
	for _, ep := range eps {
		switch ep.Status {
		case store.EpDone:
			card.DoneEpisodes++
		case store.EpFailed:
			card.FailedEpisodes++
		}
	}
	if live, ok := s.Downloads.LiveJob(job.ID); ok {
		card.CurrentEpisode = live.EpisodeIndex
		card.SpeedBps = live.Speed
	}
	card.DownloadedBytes, card.EstimatedTotalByte = s.Downloads.JobBytes(job)
	return card
}

// QueueSnapshot 全量队列快照（GET /downloads 与 SSE 同源）。
func (s *Server) QueueSnapshot() queueSnapshot {
	jobs, err := s.Downloads.Store().ListJobs()
	if err != nil {
		return queueSnapshot{Jobs: []downloadCard{}}
	}
	cards := make([]downloadCard, 0, len(jobs))
	for i := range jobs {
		cards = append(cards, s.jobCard(&jobs[i]))
	}
	return queueSnapshot{Jobs: cards}
}

// handleDownloadsCreate POST /api/v1/downloads {series_id, quality, episodes[]}
// episodes 空 = 整剧；幂等（同剧返回既有任务，done 补集重入队由引擎处理）。
func (s *Server) handleDownloadsCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireDownloads(w) {
		return
	}
	var req struct {
		SeriesID string `json:"series_id"`
		Quality  int    `json:"quality"`
		Episodes []int  `json:"episodes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON", err.Error())
		return
	}
	if !seriesIDPattern.MatchString(req.SeriesID) {
		writeError(w, http.StatusBadRequest, "请求参数不对", "series_id 应为 1–32 位数字（红剧集 ID）")
		return
	}
	if req.Quality < 0 {
		writeError(w, http.StatusBadRequest, "请求参数不对", "quality 需是非负整数（0 = 最高画质）")
		return
	}
	for _, ep := range req.Episodes {
		if ep < 1 {
			writeError(w, http.StatusBadRequest, "请求参数不对", "episodes 里的集号需是正整数（从 1 起）")
			return
		}
	}
	job, err := s.Downloads.AddJob(r.Context(), req.SeriesID, req.Episodes, req.Quality)
	if err != nil {
		writeError(w, http.StatusBadGateway, "创建下载任务失败，请稍后重试", err.Error())
		return
	}
	fresh, err := s.Downloads.Job(job.ID)
	if err != nil {
		fresh = job
	}
	writeJSON(w, http.StatusOK, s.jobCard(fresh))
}

// handleDownloadsList GET /api/v1/downloads —— 队列卡片（轮询兜底口径）。
func (s *Server) handleDownloadsList(w http.ResponseWriter, r *http.Request) {
	if !s.requireDownloads(w) {
		return
	}
	writeJSON(w, http.StatusOK, s.QueueSnapshot())
}

// handleDownloadEpisodes GET /api/v1/downloads/{jobID}/episodes —— 分集明细。
func (s *Server) handleDownloadEpisodes(w http.ResponseWriter, r *http.Request) {
	if !s.requireDownloads(w) {
		return
	}
	jobID, ok := parseJobID(w, r)
	if !ok {
		return
	}
	job, err := s.Downloads.Job(jobID)
	if err != nil {
		writeJobNotFound(w)
		return
	}
	eps, err := s.Downloads.Store().Episodes(job.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "分集状态读取失败", err.Error())
		return
	}
	cards := make([]episodeCard, 0, len(eps))
	for _, ep := range eps {
		errMsg, _ := s.Downloads.Store().EpisodeMeta(job.ID, ep.Index, "last_error")
		cards = append(cards, episodeCard{
			Index: ep.Index, VID: ep.VID, Status: ep.Status, Retries: ep.Retries,
			Error: errMsg, UpdatedAt: ep.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": job.ID, "episodes": cards})
}

// handleDownloadControl POST /api/v1/downloads/{jobID}/pause|resume|retry。
func (s *Server) handleDownloadControl(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.requireDownloads(w) {
			return
		}
		jobID, ok := parseJobID(w, r)
		if !ok {
			return
		}
		job, err := s.Downloads.Job(jobID)
		if err != nil {
			writeJobNotFound(w)
			return
		}
		var cerr error
		switch action {
		case "pause":
			cerr = s.Downloads.PauseJob(job.ID)
		case "resume":
			cerr = s.Downloads.ResumeJob(job.ID)
		case "retry":
			cerr = s.Downloads.RetryJob(job.ID)
		}
		if cerr != nil {
			writeError(w, http.StatusInternalServerError, "操作没有成功", cerr.Error())
			return
		}
		fresh, err := s.Downloads.Job(job.ID)
		if err != nil {
			writeJobNotFound(w)
			return
		}
		writeJSON(w, http.StatusOK, s.jobCard(fresh))
	}
}

// handleDownloadDelete DELETE /api/v1/downloads/{jobID}?keepVideo=1
// keepVideo=1/true 保留已下载视频（默认删除文件）。
func (s *Server) handleDownloadDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireDownloads(w) {
		return
	}
	jobID, ok := parseJobID(w, r)
	if !ok {
		return
	}
	job, err := s.Downloads.Job(jobID)
	if err != nil {
		writeJobNotFound(w)
		return
	}
	keep := r.URL.Query().Get("keepVideo")
	keepVideo := keep == "1" || keep == "true"
	if err := s.Downloads.DeleteJob(job.DramaID, keepVideo); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJobNotFound(w)
			return
		}
		writeError(w, http.StatusInternalServerError, "删除没有成功", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseJobID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("jobID"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "任务 ID 不对", "jobID 应是正整数")
		return 0, false
	}
	return id, true
}

func writeJobNotFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "没有这个下载任务", "它可能已被删除，刷新队列页看看")
}
