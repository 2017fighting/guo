// 播放器组件：取流（线路/画质）+ <video>/hls.js 附加 + 弹幕窗口拉取 +
// 控制条（弹幕开关/画质/线路/下一集）。画面比例自适应：按流的 vertical
// 判定（16:9 全宽 / 9:16 居中窄栏），loadedmetadata 后按视频实际尺寸校正。

import { useEffect, useRef, useState } from 'react'
import type HlsType from 'hls.js'
import { LoaderCircle, MessageSquareOff, MessageSquareText, Play, SkipForward } from 'lucide-react'
import { api, ApiError } from '@/lib/api'
import { cn } from '@/lib/utils'
import { DanmakuOverlay } from '@/components/player/danmaku-overlay'
import {
  DANMAKU_LIFETIME_MS,
  DANMAKU_WINDOW_MS,
  LINE_OPTIONS,
  type DanmakuItem,
  type DanmakuWindow,
  type StreamInfo,
} from '@/types/play'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'

const DANMAKU_CACHE_WINDOWS = 6 // 保留最近 6 窗（guoapp 口径）

function fmtClock(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) ms = 0
  const total = Math.floor(ms / 1000)
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  return h > 0
    ? `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
    : `${m}:${String(s).padStart(2, '0')}`
}

function danmakuKey(item: DanmakuItem): string {
  return item.id ?? `${item.offset_ms}:${item.text}`
}

type Phase = 'loading' | 'error' | 'ready'

export function VideoPlayer({
  seriesID,
  vid,
  hasNext,
  onNext,
}: {
  seriesID: string
  vid: string
  hasNext: boolean
  onNext: () => void
}) {
  const [line, setLine] = useState('')
  const [quality, setQuality] = useState(0)
  const [attempt, setAttempt] = useState(0)
  const [info, setInfo] = useState<StreamInfo | null>(null)
  const [phase, setPhase] = useState<Phase>('loading')
  const [error, setError] = useState<{ message: string; hint: string } | null>(null)
  const [playing, setPlaying] = useState(false)
  const [timeMS, setTimeMS] = useState(0)
  const [durationMS, setDurationMS] = useState(0)
  const [measuredVertical, setMeasuredVertical] = useState<boolean | null>(null)
  const [danmakuOn, setDanmakuOn] = useState(true)
  const [danmakuItems, setDanmakuItems] = useState<DanmakuItem[]>([])
  const [danmakuError, setDanmakuError] = useState(false)

  const videoRef = useRef<HTMLVideoElement>(null)
  const hlsRef = useRef<HlsType | null>(null)
  const pendingSeekRef = useRef<number | null>(null)
  const prevVidRef = useRef('')
  const windowsRef = useRef<Map<number, DanmakuWindow>>(new Map())

  // 取流：换集重置；换线路/画质（同集）保留进度续播
  useEffect(() => {
    let cancelled = false
    const sameEpisode = prevVidRef.current === vid
    prevVidRef.current = vid
    const resumeMS = sameEpisode && videoRef.current ? videoRef.current.currentTime * 1000 : null
    setPhase('loading')
    setError(null)
    setDanmakuItems([])
    windowsRef.current.clear()
    api<StreamInfo>(`/api/v1/drama/${seriesID}/episodes/${vid}/stream`, {
      line: line || undefined,
      quality: quality || undefined,
    })
      .then((data) => {
        if (cancelled) return
        setInfo(data)
        setMeasuredVertical(null)
        pendingSeekRef.current = resumeMS
        setPhase('ready')
      })
      .catch((err) => {
        if (cancelled) return
        if (err instanceof ApiError) setError({ message: err.message, hint: err.hint })
        else setError({ message: '取流失败，请检查网络', hint: '稍后重试或切换线路' })
        setPhase('error')
      })
    return () => {
      cancelled = true
    }
  }, [seriesID, vid, line, quality, attempt])

  // 附加媒体源：HLS 清单用 hls.js（动态加载，不进主包），其余直连 MP4
  useEffect(() => {
    const video = videoRef.current
    if (!video || !info) return
    let cancelled = false
    const url = info.proxy_url
    const attach = async () => {
      hlsRef.current?.destroy()
      hlsRef.current = null
      if (info.hls) {
        const { default: Hls } = await import('hls.js')
        if (cancelled) return
        if (Hls.isSupported()) {
          const hls = new Hls()
          hlsRef.current = hls
          hls.loadSource(url)
          hls.attachMedia(video)
          hls.on(Hls.Events.ERROR, (_event, data) => {
            if (!data.fatal) return
            // 清单拉挂/不是 HLS → 回退直连（可能是普通 MP4）
            hls.destroy()
            hlsRef.current = null
            video.src = url
            void video.play().catch(() => undefined)
          })
          return
        }
      }
      video.src = url
    }
    void attach()
    return () => {
      cancelled = true
      hlsRef.current?.destroy()
      hlsRef.current = null
    }
  }, [info])

  // 弹幕窗口：按 30s 对齐拉当前窗，剩 8s 预取下一窗（guoapp 口径）
  const danmakuDuration = info?.duration_ms || durationMS
  useEffect(() => {
    const duration = danmakuDuration
    if (!danmakuOn || !duration || duration <= 0) return
    const current = Math.floor(timeMS / DANMAKU_WINDOW_MS) * DANMAKU_WINDOW_MS
    const want = [current]
    if (current + DANMAKU_WINDOW_MS - timeMS <= DANMAKU_LIFETIME_MS) {
      want.push(current + DANMAKU_WINDOW_MS)
    }
    let cancelled = false
    for (const start of want) {
      if (start >= duration || windowsRef.current.has(start)) continue
      windowsRef.current.set(start, { items: [], next_ms: start }) // 占位防重复拉
      api<DanmakuWindow>(`/api/v1/drama/${seriesID}/episodes/${vid}/danmaku`, {
        from: start,
        duration,
      })
        .then((win) => {
          if (cancelled) return
          windowsRef.current.set(start, win)
          while (windowsRef.current.size > DANMAKU_CACHE_WINDOWS) {
            // 只保留最近 6 窗（按插入序淘汰最旧）
            const oldest = windowsRef.current.keys().next().value
            if (oldest === undefined) break
            windowsRef.current.delete(oldest)
          }
          setDanmakuItems((prev) => {
            const seen = new Set(prev.map(danmakuKey))
            const merged = [...prev]
            for (const item of win.items) {
              const key = danmakuKey(item)
              if (!seen.has(key)) {
                seen.add(key)
                merged.push(item)
              }
            }
            merged.sort((a, b) => a.offset_ms - b.offset_ms)
            return merged
          })
        })
        .catch(() => {
          if (cancelled) return
          windowsRef.current.delete(start)
          setDanmakuError(true)
        })
    }
    return () => {
      cancelled = true
    }
  }, [seriesID, vid, timeMS, danmakuOn, danmakuDuration])

  const vertical = measuredVertical ?? info?.vertical ?? true
  const togglePlay = () => {
    const video = videoRef.current
    if (!video) return
    if (video.paused) void video.play().catch(() => undefined)
    else video.pause()
  }

  if (phase === 'error') {
    return (
      <div className="flex aspect-video w-full items-center justify-center rounded-lg bg-black">
        <div className="max-w-md px-6 text-center text-white">
          <p className="font-medium">{error?.message}</p>
          <p className="mt-2 text-sm text-white/70">{error?.hint}</p>
          <button
            type="button"
            className="mt-4 rounded-md border border-white/30 px-3 py-1.5 text-sm hover:bg-white/10"
            onClick={() => {
              setLine('')
              setQuality(0)
              setAttempt((n) => n + 1)
            }}
          >
            重试
          </button>
        </div>
      </div>
    )
  }

  const seekMaxSec = durationMS > 0 ? durationMS / 1000 : 0.1

  return (
    <div
      className={cn(
        'relative mx-auto overflow-hidden rounded-lg bg-black',
        vertical ? 'aspect-[9/16] w-full max-w-[min(405px,92vw)]' : 'aspect-video w-full',
      )}
    >
      <video
        ref={videoRef}
        className="h-full w-full"
        playsInline
        preload="metadata"
        onClick={togglePlay}
        onLoadedMetadata={(e) => {
          const video = e.currentTarget
          if (video.videoWidth > 0 && video.videoHeight > 0) {
            setMeasuredVertical(video.videoHeight > video.videoWidth)
          }
          if (pendingSeekRef.current != null) {
            video.currentTime = Math.min(
              pendingSeekRef.current / 1000,
              Math.max(0, (video.duration || 0) - 0.5),
            )
            pendingSeekRef.current = null
          }
          if (video.duration && Number.isFinite(video.duration)) setDurationMS(video.duration * 1000)
        }}
        onTimeUpdate={(e) => setTimeMS(e.currentTarget.currentTime * 1000)}
        onPlay={() => setPlaying(true)}
        onPause={() => setPlaying(false)}
        onEnded={onNext}
      />

      <DanmakuOverlay items={danmakuItems} timeMS={timeMS} enabled={danmakuOn} />

      {/* 中央播放/暂停（暂停时可见） */}
      {!playing && phase === 'ready' && (
        <button
          type="button"
          aria-label="播放/暂停"
          className="absolute inset-0 m-auto flex size-14 items-center justify-center rounded-full bg-black/55 text-white"
          onClick={togglePlay}
        >
          <Play className="size-6" aria-hidden />
        </button>
      )}
      {phase === 'loading' && (
        <div className="absolute inset-0 flex items-center justify-center">
          <LoaderCircle className="size-8 animate-spin text-white/80" aria-label="取流中" />
        </div>
      )}

      {/* 底部控制条：进度 + 弹幕开关/画质/线路/下一集 */}
      <div
        className="absolute inset-x-0 bottom-0 bg-gradient-to-t from-black/70 to-transparent px-3 pb-2 pt-6 text-white"
        onClick={(e) => e.stopPropagation()}
      >
        <input
          type="range"
          aria-label="播放进度"
          className="h-1 w-full cursor-pointer accent-white"
          min={0}
          max={seekMaxSec}
          step={0.1}
          value={Math.min(timeMS / 1000, seekMaxSec)}
          onChange={(e) => {
            const video = videoRef.current
            if (video) video.currentTime = Number(e.target.value)
          }}
        />
        <div className="mt-1.5 flex flex-wrap items-center gap-2 text-xs">
          <span className="tabular-nums">
            {fmtClock(timeMS)} / {fmtClock(durationMS)}
          </span>
          <button
            type="button"
            className="flex items-center gap-1 rounded bg-black/50 px-2 py-0.5 hover:bg-black/70"
            aria-pressed={danmakuOn}
            onClick={() => setDanmakuOn((on) => !on)}
          >
            {danmakuOn ? (
              <MessageSquareText className="size-3.5" aria-hidden />
            ) : (
              <MessageSquareOff className="size-3.5" aria-hidden />
            )}
            弹幕 {danmakuOn ? '开' : '关'}
          </button>
          <span className="rounded bg-black/50 py-0.5 pl-1 pr-1.5">
            <Select value={String(quality)} onValueChange={(v) => setQuality(Number(v))}>
              <SelectTrigger
                size="sm"
                aria-label="画质"
                className="h-auto border-0 bg-transparent px-1 text-xs text-white focus:ring-0"
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {(info?.qualities ?? [{ id: 0, label: '自动' }]).map((q) => (
                  <SelectItem key={q.id} value={String(q.id)}>
                    {q.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </span>
          <span className="rounded bg-black/50 py-0.5 pl-1 pr-1.5">
            <Select value={line || 'auto'} onValueChange={(v) => setLine(v === 'auto' ? '' : v)}>
              <SelectTrigger
                size="sm"
                aria-label="线路"
                className="h-auto border-0 bg-transparent px-1 text-xs text-white focus:ring-0"
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {LINE_OPTIONS.map((opt) => (
                  <SelectItem key={opt.value || 'auto'} value={opt.value || 'auto'}>
                    {opt.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </span>
          {info?.cenc && (
            <span
              className="rounded bg-amber-600/80 px-2 py-0.5"
              title="该线路为 CENC 加密流，浏览器可能无法解码，播不了请切线路"
            >
              加密流
            </span>
          )}
          {danmakuError && <span className="text-white/60">弹幕暂时拉不到</span>}
          <div className="flex-1" />
          <button
            type="button"
            className="flex items-center gap-1 rounded bg-black/50 px-2 py-0.5 hover:bg-black/70 disabled:opacity-40"
            disabled={!hasNext}
            onClick={onNext}
          >
            <SkipForward className="size-3.5" aria-hidden />
            下一集
          </button>
        </div>
      </div>
    </div>
  )
}
