// 播放页：播放器（比例自适应 + 弹幕 Canvas + 控制条，见 components/player）+
// 选集列表（当前高亮）+ 下载本集入口。键盘 ↑/↓ 切集。
// 交互对齐 mockups/play.html；详情/选集数据来自 #11 的 drama 接口。

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { Download, LoaderCircle } from 'lucide-react'
import { api, apiPost, ApiError } from '@/lib/api'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { VideoPlayer } from '@/components/player/video-player'
import type { DramaDetail } from '@/types/drama'
import type { DownloadJob } from '@/types/downloads'
import { episodeGroupAt, episodeGroups } from '@/lib/episode-groups'
import { EpisodeGroupTabs } from '@/components/episode-group-tabs'

export function PlayPage() {
  const { seriesID = '', vid = '' } = useParams()
  const navigate = useNavigate()
  const [detail, setDetail] = useState<DramaDetail | null>(null)
  const [detailError, setDetailError] = useState<string | null>(null)
  const [downloadMsg, setDownloadMsg] = useState<string | null>(null)
  const [downloading, setDownloading] = useState(false)

  useEffect(() => {
    setDetail(null)
    setDetailError(null)
    api<DramaDetail>(`/api/v1/drama/${seriesID}`)
      .then(setDetail)
      .catch((err) =>
        setDetailError(
          err instanceof ApiError ? `${err.message}（${err.hint}）` : '选集列表加载失败，请稍后重试',
        ),
      )
  }, [seriesID])

  const episodes = useMemo(() => detail?.episodes ?? [], [detail])
  const currentIndex = episodes.findIndex((e) => e.vid === vid)
  const current = currentIndex >= 0 ? episodes[currentIndex] : null
  const next = currentIndex >= 0 && currentIndex + 1 < episodes.length ? episodes[currentIndex + 1] : null
  const prev = currentIndex > 0 ? episodes[currentIndex - 1] : null

  const goNext = useCallback(() => {
    if (next) navigate(`/play/${seriesID}/${next.vid}`)
  }, [navigate, seriesID, next])
  const goPrev = useCallback(() => {
    if (prev) navigate(`/play/${seriesID}/${prev.vid}`)
  }, [navigate, seriesID, prev])

  // 键盘切集（↑ 上一集 / ↓ 下一集；输入框聚焦时忽略）
  const nextRef = useRef(goNext)
  const prevRef = useRef(goPrev)
  useEffect(() => {
    nextRef.current = goNext
    prevRef.current = goPrev
  }, [goNext, goPrev])
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null
      if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)) {
        return
      }
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        nextRef.current()
      } else if (e.key === 'ArrowUp') {
        e.preventDefault()
        prevRef.current()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  // 下载本集：复用 #11 建任务接口（episodes 传集号）
  const downloadCurrent = async () => {
    if (!current || downloading) return
    setDownloading(true)
    setDownloadMsg(null)
    try {
      await apiPost<DownloadJob>('/api/v1/downloads', {
        series_id: seriesID,
        quality: 0,
        episodes: [current.index],
      })
      setDownloadMsg(`第 ${current.index} 集已加入下载队列`)
    } catch (err) {
      setDownloadMsg(err instanceof ApiError ? `${err.message}（${err.hint}）` : '加入下载队列失败，请稍后重试')
    } finally {
      setDownloading(false)
    }
  }

  // 选集分组：默认跟随正在播放的集所在组；点 tab 手动切组（播放集换组时重回跟随）
  const groups = episodeGroups(episodes.length)
  const playingGroup = episodeGroupAt(groups, currentIndex + 1) ?? groups[0]
  const [groupStart, setGroupStart] = useState<number | null>(null)
  useEffect(() => {
    setGroupStart(null)
  }, [playingGroup?.start])
  const currentGroup = (groupStart != null ? episodeGroupAt(groups, groupStart) : undefined) ?? playingGroup

  return (
    <div>
      <Link to={`/drama/${seriesID}`} className="text-sm text-muted-foreground hover:text-foreground">
        ← 返回详情{detail?.title ? ` · ${detail.title}` : ''}
      </Link>

      <div className="mt-4">
        <VideoPlayer seriesID={seriesID} vid={vid} hasNext={next != null} onNext={goNext} />
      </div>

      <div className="mt-3 text-xs text-muted-foreground">
        {prev ? (
          <button type="button" className="hover:text-foreground" onClick={goPrev}>
            上一集
          </button>
        ) : (
          <span className="opacity-50">上一集</span>
        )}
        {' · '}
        <strong className="text-foreground">{current ? `第 ${current.index} 集 · 正在播放` : '正在播放'}</strong>
        {' · '}
        {next ? (
          <button type="button" className="hover:text-foreground" onClick={goNext}>
            下一集 →
          </button>
        ) : (
          <span className="opacity-50">已是最新集</span>
        )}
        （上下键切集）
      </div>

      {/* 选集：当前高亮 + 下载本集 */}
      <Card className="mt-4 py-4">
        <CardContent className="px-4">
          <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
            <h2 className="text-lg font-semibold">
              选集{' '}
              {currentGroup && (
                <span className="text-sm font-normal text-muted-foreground">
                  第 {currentGroup.start}–{currentGroup.end} 集
                </span>
              )}
            </h2>
            <div className="flex items-center gap-2">
              {downloadMsg && <span className="text-xs text-muted-foreground">{downloadMsg}</span>}
              <Button
                size="sm"
                variant="outline"
                className="gap-1"
                disabled={!current || downloading}
                onClick={downloadCurrent}
              >
                {downloading ? (
                  <LoaderCircle className="size-3.5 animate-spin" aria-hidden />
                ) : (
                  <Download className="size-3.5" aria-hidden />
                )}
                下载本集
              </Button>
            </div>
          </div>
          {groups.length > 0 && (
            <div className="mb-3">
              <EpisodeGroupTabs groups={groups} active={currentGroup} onSelect={setGroupStart} />
            </div>
          )}
          {detailError ? (
            <p className="py-2 text-sm text-muted-foreground">{detailError}</p>
          ) : episodes.length === 0 ? (
            <p className="py-2 text-sm text-muted-foreground">选集加载中…</p>
          ) : (
            <div className="grid grid-cols-[repeat(auto-fill,minmax(44px,1fr))] gap-2">
              {(currentGroup
                ? episodes.filter((e) => e.index >= currentGroup.start && e.index <= currentGroup.end)
                : episodes
              ).map((ep) => (
                <Link
                  key={ep.index}
                  to={`/play/${seriesID}/${ep.vid}`}
                  title={`第 ${ep.index} 集`}
                  className={cn(
                    'flex min-h-11 items-center justify-center rounded-md border text-sm hover:border-ring hover:bg-accent',
                    ep.vid === vid
                      ? 'border-primary bg-primary text-primary-foreground hover:border-primary hover:bg-primary'
                      : 'text-muted-foreground',
                  )}
                  aria-current={ep.vid === vid ? 'true' : undefined}
                >
                  {ep.index}
                </Link>
              ))}
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
