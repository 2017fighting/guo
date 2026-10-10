// 下载队列页：任务=剧卡片三态（下载中/完成/失败），分集明细折叠，
// 暂停/继续/重试失败集/更新本剧/删除（确认+保留视频）。SSE 实时 + 轮询兜底。
// 交互对齐 mockups/downloads.html。

import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { CheckCircle2, LoaderCircle } from 'lucide-react'
import { api, apiDelete, apiPost, ApiError } from '@/lib/api'
import { formatBytes, formatEta, formatSpeed } from '@/lib/format'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Modal } from '@/components/modal'
import type {
  DownloadEpisode,
  DownloadEpisodesResponse,
  DownloadJob,
  QueueSnapshot,
} from '@/types/downloads'

const POLL_INTERVAL_MS = 3000

export function DownloadsPage() {
  const [jobs, setJobs] = useState<DownloadJob[]>([])
  const [polling, setPolling] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<{ message: string; hint: string } | null>(null)
  const [deleting, setDeleting] = useState<DownloadJob | null>(null)

  const refresh = useCallback(async () => {
    try {
      const snapshot = await api<QueueSnapshot>('/api/v1/downloads')
      setJobs(snapshot.jobs)
      setError(null)
    } catch (err) {
      if (err instanceof ApiError) setError({ message: err.message, hint: err.hint })
      else setError({ message: '队列状态拉取失败', hint: '请检查服务是否在运行' })
    } finally {
      setLoaded(true)
    }
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  // SSE 实时：event: queue 全量快照；断线自动降级轮询（恢复后停轮询）。
  useEffect(() => {
    const es = new EventSource('/api/v1/events')
    es.addEventListener('queue', (event) => {
      try {
        const snapshot = JSON.parse((event as MessageEvent<string>).data) as QueueSnapshot
        setJobs(snapshot.jobs)
        setError(null)
      } catch {
        // 帧损坏：等下一帧/轮询兜底
      }
    })
    es.onopen = () => setPolling(false)
    es.onerror = () => setPolling(true)
    return () => es.close()
  }, [])

  useEffect(() => {
    if (!polling) return
    const timer = window.setInterval(() => void refresh(), POLL_INTERVAL_MS)
    return () => window.clearInterval(timer)
  }, [polling, refresh])

  return (
    <div className="mx-auto max-w-[900px]">
      <div className="mb-4 flex items-center justify-between">
        <h1 className="text-xl font-semibold">下载队列</h1>
        <span className="text-xs text-muted-foreground">
          {polling ? '轮询模式（事件流不可用）' : '实时更新中'}
        </span>
      </div>

      {error && (
        <Card className="mb-4">
          <CardContent className="flex flex-col items-center gap-2 px-6 py-6 text-center">
            <p className="font-medium">{error.message}</p>
            <p className="text-sm text-muted-foreground">{error.hint}</p>
            <Button variant="outline" onClick={() => void refresh()}>
              重试
            </Button>
          </CardContent>
        </Card>
      )}

      {loaded && jobs.length === 0 && !error && (
        <Card>
          <CardContent className="px-6 py-10 text-center text-muted-foreground">
            还没有下载任务。去{' '}
            <Link to="/" className="underline hover:text-foreground">
              浏览页
            </Link>{' '}
            挑一部剧，或在详情页点「下载本剧」。
          </CardContent>
        </Card>
      )}

      {jobs.map((job) => (
        <JobCard key={job.id} job={job} onDelete={() => setDeleting(job)} />
      ))}

      {deleting && (
        <DeleteDialog
          job={deleting}
          onClose={() => setDeleting(null)}
          onDeleted={() => setDeleting(null)}
        />
      )}
    </div>
  )
}

function qualityLabel(quality: number): string {
  if (quality === 0) return '最高画质'
  return `${quality}p`
}

function JobCard({ job, onDelete }: { job: DownloadJob; onDelete: () => void }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const control = async (action: 'pause' | 'resume' | 'retry') => {
    setBusy(true)
    setError(null)
    try {
      await apiPost(`/api/v1/downloads/${job.id}/${action}`)
    } catch (err) {
      setError(err instanceof ApiError ? `${err.message}（${err.hint}）` : '操作失败，请稍后重试')
    } finally {
      setBusy(false)
    }
  }

  const updateSeries = async () => {
    // 「更新本剧」= 以整剧重发建任务：新分集补进既有任务并自动重入队
    setBusy(true)
    setError(null)
    try {
      await apiPost('/api/v1/downloads', { series_id: job.series_id, quality: job.quality })
    } catch (err) {
      setError(err instanceof ApiError ? `${err.message}（${err.hint}）` : '更新失败，请稍后重试')
    } finally {
      setBusy(false)
    }
  }

  const percent =
    job.total_episodes > 0 ? Math.round((job.done_episodes / job.total_episodes) * 100) : 0
  const remaining = job.estimated_total_bytes - job.downloaded_bytes
  const eta = job.status === 'running' ? formatEta(remaining, job.speed_bps) : ''

  return (
    <Card className="mb-4 py-4">
      <CardContent className="flex flex-wrap items-start gap-4 px-4">
        <div className="w-24 shrink-0 overflow-hidden rounded-lg border">
          {job.status === 'done' ? (
            <div className="flex aspect-[2/3] items-center justify-center bg-muted p-2 text-center text-sm font-semibold text-muted-foreground">
              {job.title.slice(0, 6)}
            </div>
          ) : (
            <div className="flex aspect-[2/3] items-center justify-center bg-muted p-2 text-center text-sm font-semibold text-muted-foreground">
              {job.title.slice(0, 6)}
            </div>
          )}
        </div>
        <div className="min-w-[240px] flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <Link to={`/drama/${job.series_id}`} className="font-semibold hover:underline">
              {job.title}
            </Link>
            <Badge variant="secondary">{qualityLabel(job.quality)}</Badge>
            {job.status === 'running' && (
              <Badge variant="outline">
                {job.current_episode > 0
                  ? `下载中 · 第 ${job.current_episode}/${job.total_episodes} 集`
                  : '下载中…'}
              </Badge>
            )}
            {job.status === 'queued' && <Badge variant="outline">排队中</Badge>}
            {job.status === 'paused' && <Badge variant="outline">已暂停</Badge>}
            {job.status === 'done' && (
              <Badge variant="outline" className="gap-1">
                <CheckCircle2 className="size-3" aria-hidden />
                已完成 · {job.done_episodes}/{job.total_episodes} 集
                {job.jellyfin_refreshed && ' · Jellyfin 已刷新'}
              </Badge>
            )}
            {job.status === 'failed' && (
              <Badge variant="destructive">
                失败 · {job.done_episodes}/{job.total_episodes} 集
              </Badge>
            )}
          </div>

          {/* 确定值进度（按集），字节比例仅在有估值时展示 */}
          <div
            className="mt-4 h-2 w-full overflow-hidden rounded-full bg-muted"
            role="progressbar"
            aria-valuenow={percent}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-label={`整体进度 ${percent}%`}
          >
            <div
              className={`h-full rounded-full transition-all ${job.status === 'failed' ? 'bg-destructive' : 'bg-primary'}`}
              style={{ width: `${percent}%` }}
            />
          </div>
          <div className="mt-2 flex flex-wrap justify-between gap-2 text-xs text-muted-foreground">
            <span>
              整体 {job.done_episodes}/{job.total_episodes} 集
              {job.estimated_total_bytes > 0 &&
                ` · ${formatBytes(job.downloaded_bytes)} / ${formatBytes(job.estimated_total_bytes)}`}
              {job.failed_episodes > 0 && ` · ${job.failed_episodes} 集失败`}
            </span>
            {(job.speed_bps > 0 || eta) && (
              <span>
                {[formatSpeed(job.speed_bps), eta].filter(Boolean).join(' · ')}
              </span>
            )}
          </div>

          {job.status === 'failed' && <FailureNote job={job} />}

          {error && <p className="mt-2 text-sm text-destructive">{error}</p>}

          <div className="mt-4 flex flex-wrap gap-2">
            {busy && <LoaderCircle className="size-4 animate-spin self-center text-muted-foreground" aria-label="处理中" />}
            {(job.status === 'running' || job.status === 'queued') && (
              <Button variant="outline" size="sm" disabled={busy} onClick={() => void control('pause')}>
                ⏸ 暂停
              </Button>
            )}
            {job.status === 'paused' && (
              <Button variant="outline" size="sm" disabled={busy} onClick={() => void control('resume')}>
                ▶ 继续
              </Button>
            )}
            {job.failed_episodes > 0 && (
              <Button
                variant={job.status === 'failed' ? 'default' : 'outline'}
                size="sm"
                disabled={busy}
                onClick={() => void control('retry')}
              >
                重试失败集{job.failed_episodes > 0 ? `（${job.failed_episodes}）` : ''}
              </Button>
            )}
            {(job.status === 'done' || job.status === 'failed') && (
              <Button variant="outline" size="sm" disabled={busy} onClick={() => void updateSeries()}>
                更新本剧
              </Button>
            )}
            <Button variant="destructive" size="sm" onClick={onDelete}>
              删除
            </Button>
          </div>

          <EpisodeDetail jobId={job.id} total={job.total_episodes} />
        </div>
      </CardContent>
    </Card>
  )
}

// 失败原因 + 出路（H1 状态可见 + H5 纠错：说明出路，不只颜色）。
function FailureNote({ job }: { job: DownloadJob }) {
  return (
    <p className="mt-3 text-sm text-muted-foreground">
      有 {job.failed_episodes} 集下载失败：可能是源站流地址过期或网络波动。点「重试失败集」重新下载，或删除任务后重新添加；
      展开分集明细可看每集的具体原因。
    </p>
  )
}

// 分集明细折叠：✓ 已完成 · ↓ 正在下载 · 数字 待下载；失败集红点 + 原因。
function EpisodeDetail({ jobId, total }: { jobId: number; total: number }) {
  const [episodes, setEpisodes] = useState<DownloadEpisode[] | null>(null)
  const [loading, setLoading] = useState(false)
  const [open, setOpen] = useState(false)

  useEffect(() => {
    if (!open || episodes || loading) return
    setLoading(true)
    api<DownloadEpisodesResponse>(`/api/v1/downloads/${jobId}/episodes`)
      .then((resp) => setEpisodes(resp.episodes))
      .catch(() => setEpisodes([]))
      .finally(() => setLoading(false))
  }, [open, episodes, loading, jobId])

  // SSE 期间自动刷新明细（已展开时）
  useEffect(() => {
    if (!open) return
    const timer = window.setInterval(() => {
      api<DownloadEpisodesResponse>(`/api/v1/downloads/${jobId}/episodes`)
        .then((resp) => setEpisodes(resp.episodes))
        .catch(() => {})
    }, POLL_INTERVAL_MS)
    return () => window.clearInterval(timer)
  }, [open, jobId])

  if (total === 0) return null

  return (
    <details className="mt-4" open={open} onToggle={(e) => setOpen((e.target as HTMLDetailsElement).open)}>
      <summary className="inline-flex min-h-8 cursor-pointer items-center text-sm text-muted-foreground">
        分集明细
      </summary>
      <div className="mt-2 grid grid-cols-[repeat(auto-fill,minmax(44px,1fr))] gap-2">
        {(episodes ?? []).map((ep) => {
          let cls = 'border text-muted-foreground'
          let label = `${ep.index}`
          let title = `第 ${ep.index} 集 · 待下载`
          if (ep.status === 'done') {
            cls = 'border-primary/40 bg-primary/10 text-foreground'
            label = `✓${ep.index}`
            title = `第 ${ep.index} 集 · 已完成`
          } else if (ep.status === 'downloading' || ep.status === 'merging') {
            cls = 'border-primary bg-primary text-primary-foreground'
            label = `${ep.index}↓`
            title = `第 ${ep.index} 集 · ${ep.status === 'merging' ? '合并中' : '正在下载'}`
          } else if (ep.status === 'failed') {
            cls = 'border-destructive bg-destructive/10 text-destructive'
            label = `${ep.index}✕`
            title = `第 ${ep.index} 集 · 失败（重试 ${ep.retries} 次）${ep.error ? `：${ep.error}` : ''}`
          }
          return (
            <span key={ep.index} title={title} className={`flex min-h-9 items-center justify-center rounded-md text-sm ${cls}`}>
              {label}
            </span>
          )
        })}
        {loading && episodes === null && (
          <span className="col-span-full inline-flex items-center gap-2 text-sm text-muted-foreground">
            <LoaderCircle className="size-4 animate-spin" aria-hidden />
            加载分集…
          </span>
        )}
      </div>
      <div className="mt-2 text-xs text-muted-foreground">✓ 已完成 · ↓ 正在下载 · ✕ 失败 · 数字 待下载</div>
    </details>
  )
}

// 删除确认：不可逆动作走确认弹层；可选保留视频（默认删除文件）。
function DeleteDialog({
  job,
  onClose,
  onDeleted,
}: {
  job: DownloadJob
  onClose: () => void
  onDeleted: () => void
}) {
  const [deleteFiles, setDeleteFiles] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const confirm = async () => {
    setBusy(true)
    setError(null)
    try {
      await apiDelete(`/api/v1/downloads/${job.id}`, { keepVideo: deleteFiles ? '' : '1' })
      onDeleted()
    } catch (err) {
      setError(err instanceof ApiError ? `${err.message}（${err.hint}）` : '删除失败，请稍后重试')
      setBusy(false)
    }
  }

  return (
    <Modal title="删除下载任务？" onClose={onClose}>
      <p className="text-sm text-muted-foreground">「{job.title}」将从队列移除。</p>
      <label className="flex min-h-8 items-center gap-2 text-sm">
        <input
          type="checkbox"
          checked={deleteFiles}
          onChange={(e) => setDeleteFiles(e.target.checked)}
          style={{ width: 18, height: 18 }}
        />
        同时删除已下载的视频文件
      </label>
      {error && <p className="text-sm text-destructive">{error}</p>}
      <div className="flex justify-between gap-2">
        <Button variant="outline" onClick={onClose}>
          保留任务
        </Button>
        <Button variant="destructive" onClick={() => void confirm()} disabled={busy} className="gap-2">
          {busy && <LoaderCircle className="size-4 animate-spin" aria-hidden />}
          确认删除
        </Button>
      </div>
    </Modal>
  )
}