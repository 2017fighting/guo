// 下载队列类型 —— 与后端 internal/server/downloads.go 卡片/分集载荷一一对应。
// estimated_total_bytes / speed_bps / current_episode 为 0 时表示空闲或未知，
// 前端对应隐藏比例与剩余时间。

export type DownloadStatus = 'queued' | 'running' | 'paused' | 'done' | 'failed'

export type EpisodeStatus = 'pending' | 'downloading' | 'merging' | 'done' | 'failed'

export interface DownloadJob {
  id: number
  drama_id: string
  series_id: string
  title: string
  year: string
  quality: number
  status: DownloadStatus
  total_episodes: number
  done_episodes: number
  failed_episodes: number
  current_episode: number
  downloaded_bytes: number
  estimated_total_bytes: number
  speed_bps: number
  jellyfin_refreshed: boolean
  created_at: string
  updated_at: string
}

export interface QueueSnapshot {
  jobs: DownloadJob[]
}

export interface DownloadEpisode {
  index: number
  vid: string
  status: EpisodeStatus
  retries: number
  error: string
  updated_at: string
}

export interface DownloadEpisodesResponse {
  job_id: number
  episodes: DownloadEpisode[]
}
