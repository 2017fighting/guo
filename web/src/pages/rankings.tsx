// 排行榜页：横向 8 榜 Tab + rank 角标海报墙 + 游标无限加载 + 数据更新时间。
// 模式对齐浏览页（seq 丢弃过期响应、哨兵翻页 + 底部按钮兜底）。

import { useCallback, useEffect, useRef, useState } from 'react'
import { LoaderCircle } from 'lucide-react'
import { api, ApiError } from '@/lib/api'
import { RankPosterWall } from '@/components/rank-poster-card'
import { PosterWallSkeleton } from '@/components/poster-wall'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { RANKING_BOARDS, type RankingItem, type RankingsPage } from '@/types/rankings'

type Phase = 'loading' | 'error' | 'ready'

interface FeedState {
  items: RankingItem[]
  offset: number
  sessionId: string
  hasMore: boolean
  updatedAt: number
}

const EMPTY_FEED: FeedState = { items: [], offset: 0, sessionId: '', hasMore: true, updatedAt: 0 }

function formatUpdatedAt(unix: number): string {
  if (!unix) return ''
  const date = new Date(unix * 1000)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

export function RankingsPage() {
  const [board, setBoard] = useState(RANKING_BOARDS[0].id)
  const [phase, setPhase] = useState<Phase>('loading')
  const [error, setError] = useState<{ message: string; hint: string } | null>(null)
  const [feed, setFeed] = useState<FeedState>(EMPTY_FEED)
  const [loadingMore, setLoadingMore] = useState(false)

  const seqRef = useRef(0)
  const sentinelRef = useRef<HTMLDivElement | null>(null)

  const fetchPage = useCallback(
    async (targetBoard: string, offset: number, sessionId: string, seq: number) => {
      try {
        const page = await api<RankingsPage>('/api/v1/rankings', {
          list: targetBoard,
          offset,
          session_id: sessionId,
        })
        if (seq !== seqRef.current) return // 已过期响应（榜单已切换），丢弃
        setFeed((prev) => {
          const base = offset === 0 ? [] : prev.items
          return {
            items: dedup([...base, ...page.items]),
            offset: page.offset,
            sessionId: page.session_id,
            hasMore: page.has_more,
            updatedAt: page.updated_at,
          }
        })
        setPhase('ready')
        setError(null)
      } catch (err) {
        if (seq !== seqRef.current) return
        if (err instanceof ApiError) {
          setError({ message: err.message, hint: err.hint })
        } else {
          setError({ message: '网络不给力，榜单加载失败', hint: '请检查服务是否在运行，稍后重试' })
        }
        setPhase('error')
      }
    },
    [],
  )

  // 切榜：重置游标从头拉
  useEffect(() => {
    const seq = ++seqRef.current
    setFeed(EMPTY_FEED)
    setPhase('loading')
    setError(null)
    void fetchPage(board, 0, '', seq)
  }, [board, fetchPage])

  const loadMore = useCallback(() => {
    if (loadingMore || phase !== 'ready' || !feed.hasMore) return
    const seq = seqRef.current
    setLoadingMore(true)
    fetchPage(board, feed.offset, feed.sessionId, seq).finally(() => {
      if (seq === seqRef.current) setLoadingMore(false)
    })
  }, [loadingMore, phase, feed, board, fetchPage])

  // 无限加载：哨兵进入视口即翻页；底部「加载更多」按钮兜底
  useEffect(() => {
    const node = sentinelRef.current
    if (!node) return
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) loadMore()
      },
      { rootMargin: '600px 0px' },
    )
    observer.observe(node)
    return () => observer.disconnect()
  }, [loadMore])

  return (
    <div>
      {/* 8 榜横向 Tab（窄屏横向滚动）+ 数据更新时间 */}
      <div className="mb-4 flex items-center gap-3">
        <Tabs value={board} onValueChange={setBoard}>
          <TabsList aria-label="榜单" className="max-w-full overflow-x-auto">
            {RANKING_BOARDS.map((b) => (
              <TabsTrigger key={b.id} value={b.id} className="px-3">
                {b.label}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
        <div className="flex-1" />
        {phase === 'ready' && feed.updatedAt > 0 && (
          <span className="hidden shrink-0 text-xs text-muted-foreground sm:inline">
            数据更新于 {formatUpdatedAt(feed.updatedAt)}
          </span>
        )}
      </div>
      {phase === 'ready' && feed.updatedAt > 0 && (
        <p className="mb-3 text-xs text-muted-foreground sm:hidden">
          数据更新于 {formatUpdatedAt(feed.updatedAt)}
        </p>
      )}

      {phase === 'loading' && feed.items.length === 0 ? (
        <PosterWallSkeleton />
      ) : phase === 'error' ? (
        <Card className="mx-auto max-w-md">
          <CardContent className="flex flex-col items-center gap-2 px-6 py-8 text-center">
            <p className="font-medium">{error?.message}</p>
            <p className="text-sm text-muted-foreground">{error?.hint}</p>
            <Button variant="outline" onClick={() => setBoard((b) => b)}>
              重试
            </Button>
          </CardContent>
        </Card>
      ) : feed.items.length === 0 ? (
        <Card className="mx-auto max-w-md">
          <CardContent className="px-6 py-8 text-center text-muted-foreground">
            榜单暂时没有内容，稍后再来看看
          </CardContent>
        </Card>
      ) : (
        <>
          <RankPosterWall items={feed.items} />
          <div ref={sentinelRef} className="h-1" aria-hidden />
          <div className="flex flex-col items-center gap-1 py-6">
            {loadingMore ? (
              <span className="inline-flex items-center gap-2 text-sm text-muted-foreground">
                <LoaderCircle className="size-4 animate-spin" aria-hidden />
                正在加载…
              </span>
            ) : feed.hasMore ? (
              <Button variant="outline" onClick={loadMore}>
                加载更多
              </Button>
            ) : (
              <span className="text-sm text-muted-foreground">已经到底啦</span>
            )}
          </div>
        </>
      )}
    </div>
  )
}

function dedup(items: readonly RankingItem[]): RankingItem[] {
  const seen = new Set<string>()
  const out: RankingItem[] = []
  for (const item of items) {
    if (seen.has(item.series_id)) continue
    seen.add(item.series_id)
    out.push(item)
  }
  return out
}
