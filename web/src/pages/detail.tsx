// 详情页：资料卡 + 标签 + 可展开简介 + 选集分组 + 立即播放（占位路由）+
// 下载弹层（画质 3 档默认最高 + 分集多选分组）。交互对齐 mockups/detail.html。

import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { LoaderCircle, Play, Download } from 'lucide-react'
import { api, apiPost, ApiError } from '@/lib/api'
import { episodeGroupAt, episodeGroups, episodesInGroup } from '@/lib/episode-groups'
import { EpisodeGroupTabs } from '@/components/episode-group-tabs'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Modal } from '@/components/modal'
import type { DramaDetail } from '@/types/drama'
import type { DownloadJob } from '@/types/downloads'

const QUALITY_OPTIONS: ReadonlyArray<{ value: number; label: string }> = [
  { value: 0, label: '最高（1080p）' },
  { value: 720, label: '720p' },
  { value: 480, label: '480p' },
]

type Phase = 'loading' | 'error' | 'ready'

export function DramaPage() {
  const { seriesID = '' } = useParams()
  const navigate = useNavigate()
  const [phase, setPhase] = useState<Phase>('loading')
  const [error, setError] = useState<{ message: string; hint: string } | null>(null)
  const [detail, setDetail] = useState<DramaDetail | null>(null)
  const [plotOpen, setPlotOpen] = useState(false)
  const [groupStart, setGroupStart] = useState(1) // 选集分组起始集号
  const [downloadOpen, setDownloadOpen] = useState(false)

  useEffect(() => {
    setPhase('loading')
    api<DramaDetail>(`/api/v1/drama/${seriesID}`)
      .then((d) => {
        setDetail(d)
        setPhase('ready')
      })
      .catch((err) => {
        if (err instanceof ApiError) setError({ message: err.message, hint: err.hint })
        else setError({ message: '网络不给力，详情加载失败', hint: '请检查服务是否在运行，稍后重试' })
        setPhase('error')
      })
  }, [seriesID])

  const groups = useMemo(() => episodeGroups(detail?.episodes.length ?? 0), [detail])

  const currentGroup = episodeGroupAt(groups, groupStart) ?? groups[0]
  const groupEpisodes = useMemo(
    () => episodesInGroup(detail?.episodes ?? [], currentGroup),
    [detail, currentGroup],
  )

  if (phase === 'loading') {
    return (
      <div className="flex justify-center py-24">
        <LoaderCircle className="size-6 animate-spin text-muted-foreground" aria-label="加载中" />
      </div>
    )
  }
  if (phase === 'error' || !detail) {
    return (
      <Card className="mx-auto max-w-md">
        <CardContent className="flex flex-col items-center gap-2 px-6 py-8 text-center">
          <p className="font-medium">{error?.message}</p>
          <p className="text-sm text-muted-foreground">{error?.hint}</p>
          <Button variant="outline" onClick={() => setPhase('loading')}>
            重试
          </Button>
        </CardContent>
      </Card>
    )
  }

  const firstVid = detail.episodes[0]?.vid ?? ''

  return (
    <div>
      <Link to="/" className="text-sm text-muted-foreground hover:text-foreground">
        ← 返回浏览
      </Link>

      {/* 资料卡：封面 + 元信息（同一卡片内成组） */}
      <Card className="mt-4 py-4">
        <CardContent className="flex flex-wrap items-start gap-4 px-4">
          <div className="w-[150px] shrink-0 overflow-hidden rounded-lg border">
            {detail.cover ? (
              <img
                src={detail.cover}
                alt={detail.title}
                className="aspect-[2/3] w-full object-cover"
                onError={(e) => e.currentTarget.remove()}
              />
            ) : (
              <div className="flex aspect-[2/3] items-center justify-center bg-muted p-3 text-center text-sm font-semibold text-muted-foreground">
                {detail.title}
              </div>
            )}
          </div>
          <div className="min-w-[260px] flex-1">
            <h1 className="text-xl font-semibold">{detail.title}</h1>
            <div className="mt-4 flex flex-wrap gap-2">
              {detail.year && <Badge>{detail.year}</Badge>}
              <Badge>{detail.episodes.length} 集</Badge>
              {detail.genres.map((tag) => (
                <Badge key={tag}>{tag}</Badge>
              ))}
            </div>
            {detail.plot && (
              <>
                <p
                  className={`mt-4 text-sm leading-relaxed text-muted-foreground ${plotOpen ? '' : 'line-clamp-3'}`}
                >
                  {detail.plot}
                </p>
                <Button
                  variant="ghost"
                  size="sm"
                  className="mt-2 px-0 text-muted-foreground"
                  aria-expanded={plotOpen}
                  onClick={() => setPlotOpen((open) => !open)}
                >
                  {plotOpen ? '收起简介' : '展开简介'}
                </Button>
              </>
            )}
            <div className="mt-6 flex flex-wrap gap-2">
              <Button asChild style={{ minWidth: 140 }} className="gap-2">
                <Link to={`/play/${detail.series_id}/${firstVid}`} aria-disabled={!firstVid}>
                  <Play data-icon="inline-start" aria-hidden />
                  立即播放
                </Link>
              </Button>
              <Button variant="outline" className="gap-2" onClick={() => setDownloadOpen(true)}>
                <Download data-icon="inline-start" aria-hidden />
                下载本剧
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>

      {/* 选集：分组 50/组，格 ≥44px；分组 tab 单行横向滚动，激活组自动滚入视野 */}
      <Card className="mt-4 py-4">
        <CardContent className="px-4">
          <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
            <h2 className="text-lg font-semibold">
              选集{' '}
              {currentGroup && (
                <span className="text-sm font-normal text-muted-foreground">
                  第 {currentGroup.start}–{currentGroup.end} 集
                </span>
              )}
            </h2>
          </div>
          <div className="mb-3">
            <EpisodeGroupTabs groups={groups} active={currentGroup} onSelect={setGroupStart} />
          </div>
          <div className="grid max-h-[340px] grid-cols-[repeat(auto-fill,minmax(44px,1fr))] gap-2 overflow-y-auto">
            {groupEpisodes.map((ep) => (
              <Link
                key={ep.index}
                to={`/play/${detail.series_id}/${ep.vid}`}
                title={`第 ${ep.index} 集`}
                className="flex min-h-11 items-center justify-center rounded-md border text-sm hover:border-ring hover:bg-accent"
              >
                {ep.index}
              </Link>
            ))}
          </div>
        </CardContent>
      </Card>

      {downloadOpen && (
        <DownloadDialog
          detail={detail}
          onClose={() => setDownloadOpen(false)}
          onEnqueued={() => navigate('/downloads')}
        />
      )}
    </div>
  )
}

function Badge({ children }: { children: React.ReactNode }) {
  return (
    <span className="rounded-full border px-2.5 py-0.5 text-xs text-muted-foreground">{children}</span>
  )
}

// 下载弹层：画质三档（默认最高）+ 分集多选（分组）；体积预估依赖源端分集体积
// 数据（当前详情接口未含），故仅展示已选集数。
function DownloadDialog({
  detail,
  onClose,
  onEnqueued,
}: {
  detail: DramaDetail
  onClose: () => void
  onEnqueued: () => void
}) {
  const [quality, setQuality] = useState(0)
  const [selected, setSelected] = useState<Set<number>>(() => new Set(detail.episodes.map((e) => e.index)))
  const [groupStart, setGroupStart] = useState(1)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const groups = episodeGroups(detail.episodes.length)
  const currentGroup = episodeGroupAt(groups, groupStart) ?? groups[0]
  const groupEpisodes = episodesInGroup(detail.episodes, currentGroup)

  const toggle = useCallback((index: number) => {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(index)) next.delete(index)
      else next.add(index)
      return next
    })
  }, [])

  const submit = async () => {
    if (selected.size === 0 || submitting) return
    setSubmitting(true)
    setError(null)
    try {
      await apiPost<DownloadJob>('/api/v1/downloads', {
        series_id: detail.series_id,
        quality,
        episodes: [...selected].sort((a, b) => a - b),
      })
      onEnqueued()
    } catch (err) {
      setError(err instanceof ApiError ? `${err.message}（${err.hint}）` : '加入队列失败，请稍后重试')
      setSubmitting(false)
    }
  }

  return (
    <Modal title={`下载「${detail.title}」`} onClose={onClose}>
      <div>
        <div className="mb-2 text-sm font-medium">画质</div>
        <div className="flex gap-2">
          {QUALITY_OPTIONS.map((opt) => (
            <Button
              key={opt.value}
              size="sm"
              variant={quality === opt.value ? 'default' : 'outline'}
              onClick={() => setQuality(opt.value)}
            >
              {opt.label}
            </Button>
          ))}
        </div>
      </div>
      <div>
        <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
          <span className="text-sm font-medium">分集</span>
          <div className="flex gap-2">
            <Button size="sm" variant="secondary" onClick={() => setSelected(new Set(detail.episodes.map((e) => e.index)))}>
              全选 {detail.episodes.length} 集
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setSelected(new Set())}>
              全不选
            </Button>
          </div>
        </div>
        <div className="mb-2 flex flex-wrap gap-2">
          {groups.map((g) => (
            <Button
              key={g.start}
              size="sm"
              variant={g.start === currentGroup.start ? 'secondary' : 'outline'}
              onClick={() => setGroupStart(g.start)}
            >
              {g.start === g.end ? `${g.start}` : `${g.start}–${g.end}`}
            </Button>
          ))}
        </div>
        <div className="grid max-h-[200px] grid-cols-[repeat(auto-fill,minmax(44px,1fr))] gap-2 overflow-auto p-0.5">
          {groupEpisodes.map((ep) => {
            const on = selected.has(ep.index)
            return (
              <button
                key={ep.index}
                type="button"
                aria-pressed={on}
                title={`第 ${ep.index} 集`}
                onClick={() => toggle(ep.index)}
                className={`flex min-h-11 items-center justify-center rounded-md border text-sm ${
                  on ? 'border-primary bg-primary text-primary-foreground' : 'text-muted-foreground'
                }`}
              >
                {ep.index}
              </button>
            )
          })}
        </div>
        <div className="mt-2 text-xs text-muted-foreground">已选 {selected.size} 集</div>
      </div>
      {error && <p className="text-sm text-destructive">{error}</p>}
      <div className="flex justify-between gap-2">
        <Button variant="outline" onClick={onClose}>
          取消
        </Button>
        <Button onClick={submit} disabled={selected.size === 0 || submitting} className="gap-2">
          {submitting && <LoaderCircle className="size-4 animate-spin" aria-hidden />}
          加入下载队列
        </Button>
      </div>
    </Modal>
  )
}
