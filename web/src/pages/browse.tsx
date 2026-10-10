// 浏览页：类型 Tabs + 排序下拉 + 可展开筛选面板 + 海报墙 + 无限加载。
// 交互对齐 mockups/browse.html；排序/过滤为客户端语义（guoapp catalog_sort）。

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { LoaderCircle } from 'lucide-react'
import { api, ApiError } from '@/lib/api'
import {
  EMPTY_FILTERS,
  filterCatalogItems,
  hasActiveFilters,
  SORT_OPTIONS,
  sortCatalogItems,
  type CatalogFilterState,
  type SortKey,
} from '@/lib/catalog-sort'
import type { CatalogFilters, CatalogItem, CatalogPage, GenreKey } from '@/types/catalog'
import { PosterWall, PosterWallSkeleton } from '@/components/poster-wall'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'

const GENRES: ReadonlyArray<{ key: GenreKey; label: string }> = [
  { key: 'all', label: '全部' },
  { key: 'short_play', label: '真人剧' },
  { key: 'comic_series', label: '漫剧' },
  { key: 'ai_series', label: 'AI 剧' },
]

type Phase = 'loading' | 'error' | 'ready'

interface FeedState {
  items: CatalogItem[]
  offset: number
  sessionId: string
  hasMore: boolean
}

const EMPTY_FEED: FeedState = { items: [], offset: 0, sessionId: '', hasMore: true }

export function BrowsePage() {
  const [genre, setGenre] = useState<GenreKey>('all')
  const [sort, setSort] = useState<SortKey>('default')
  const [filterState, setFilterState] = useState<CatalogFilterState>(EMPTY_FILTERS)
  const [filterEnums, setFilterEnums] = useState<CatalogFilters | null>(null)
  const [panelOpen, setPanelOpen] = useState(false)
  const [phase, setPhase] = useState<Phase>('loading')
  const [error, setError] = useState<{ message: string; hint: string } | null>(null)
  const [feed, setFeed] = useState<FeedState>(EMPTY_FEED)
  const [loadingMore, setLoadingMore] = useState(false)

  const seqRef = useRef(0)
  const sentinelRef = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    api<CatalogFilters>('/api/v1/catalog/filters')
      .then(setFilterEnums)
      .catch(() => {
        // 筛选项拉不到不阻塞浏览；面板按钮仍可用，只是没有题材行数据
      })
  }, [])

  const fetchPage = useCallback(
    async (targetGenre: GenreKey, offset: number, sessionId: string, seq: number) => {
      try {
        const page = await api<CatalogPage>('/api/v1/catalog', {
          genre: targetGenre,
          offset,
          session_id: sessionId,
        })
        if (seq !== seqRef.current) return // 已过期响应（genre 已切换），丢弃
        setFeed((prev) => {
          if (offset === 0) {
            return { items: dedup(page.items), offset: page.offset, sessionId: page.session_id, hasMore: page.has_more }
          }
          return {
            items: dedup([...prev.items, ...page.items]),
            offset: page.offset,
            sessionId: page.session_id,
            hasMore: page.has_more,
          }
        })
        setPhase('ready')
        setError(null)
      } catch (err) {
        if (seq !== seqRef.current) return
        if (err instanceof ApiError) {
          setError({ message: err.message, hint: err.hint })
        } else {
          setError({ message: '网络不给力，内容加载失败', hint: '请检查服务是否在运行，稍后重试' })
        }
        setPhase('error')
      }
    },
    [],
  )

  // 切类型：重置游标从头拉（丢弃过期响应）
  useEffect(() => {
    const seq = ++seqRef.current
    setFeed(EMPTY_FEED)
    setPhase('loading')
    setError(null)
    void fetchPage(genre, 0, '', seq)
  }, [genre, fetchPage])

  const loadMore = useCallback(() => {
    if (loadingMore || phase !== 'ready' || !feed.hasMore) return
    const seq = seqRef.current
    setLoadingMore(true)
    fetchPage(genre, feed.offset, feed.sessionId, seq).finally(() => {
      if (seq === seqRef.current) setLoadingMore(false)
    })
  }, [loadingMore, phase, feed, genre, fetchPage])

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

  const visible = useMemo(
    () => filterCatalogItems(sortCatalogItems(feed.items, sort), filterState, filterEnums),
    [feed.items, sort, filterState, filterEnums],
  )

  const activeFilterCount = hasActiveFilters(filterState) ? 1 : 0

  return (
    <div>
      {/* 类型 Tabs + 排序 + 筛选开关 */}
      <div className="mb-4 flex flex-wrap items-center gap-2">
        <Tabs value={genre} onValueChange={(value) => setGenre(value as GenreKey)}>
          <TabsList aria-label="内容类型">
            {GENRES.map((g) => (
              <TabsTrigger key={g.key} value={g.key}>
                {g.label}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
        <div className="flex-1" />
        <label className="sr-only" htmlFor="sort">
          排序方式
        </label>
        <Select value={sort} onValueChange={(value) => setSort(value as SortKey)}>
          <SelectTrigger id="sort" className="w-auto min-w-28" aria-label="排序方式">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {SORT_OPTIONS.map((option) => (
              <SelectItem key={option.key} value={option.key}>
                {option.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Button
          variant="outline"
          size="sm"
          aria-expanded={panelOpen}
          onClick={() => setPanelOpen((open) => !open)}
        >
          筛选 {panelOpen ? '▴' : '▾'}
          {activeFilterCount > 0 && <span className="text-muted-foreground">（生效中）</span>}
        </Button>
      </div>

      {/* 高级筛选（默认收起，渐进披露） */}
      {panelOpen && (
        <Card className="mb-4 py-3">
          <CardContent className="flex flex-col gap-2 px-4">
            <FilterRow
              label="题材"
              options={filterEnums?.themes ?? []}
              value={filterState.theme}
              emptyHint="筛选项暂时拉不到，稍后再试"
              onChange={(theme) => setFilterState((state) => ({ ...state, theme }))}
            />
            <FilterRow
              label="状态"
              options={filterEnums?.statuses ?? []}
              value={filterState.status}
              emptyHint="全部 / 连载中 / 已完结"
              onChange={(status) => setFilterState((state) => ({ ...state, status }))}
            />
            <FilterRow
              label="上线"
              options={filterEnums?.online_times ?? []}
              value={filterState.onlineTime}
              emptyHint="不限 / 今日上新 / 本周 / 本月"
              onChange={(onlineTime) => setFilterState((state) => ({ ...state, onlineTime }))}
            />
          </CardContent>
        </Card>
      )}

      {/* 海报墙 + 状态 */}
      {phase === 'loading' && feed.items.length === 0 ? (
        <PosterWallSkeleton />
      ) : phase === 'error' ? (
        <Card className="mx-auto max-w-md">
          <CardContent className="flex flex-col items-center gap-2 px-6 py-8 text-center">
            <p className="font-medium">{error?.message}</p>
            <p className="text-sm text-muted-foreground">{error?.hint}</p>
            <Button variant="outline" onClick={() => setGenre((g) => g)}>
              重试
            </Button>
          </CardContent>
        </Card>
      ) : visible.length === 0 ? (
        <Card className="mx-auto max-w-md">
          <CardContent className="px-6 py-8 text-center text-muted-foreground">
            {hasActiveFilters(filterState)
              ? '没有符合条件的剧集，换个筛选试试'
              : '暂时没有内容，稍后再来看看'}
          </CardContent>
        </Card>
      ) : (
        <>
          <PosterWall items={visible} hrefFor={(item) => `/drama/${item.series_id}`} />
          <div ref={sentinelRef} className="h-1" aria-hidden />
          <div className="flex justify-center py-6">
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

function FilterRow({
  label,
  options,
  value,
  emptyHint,
  onChange,
}: {
  label: string
  options: ReadonlyArray<{ id: string; name: string }>
  value: string
  emptyHint: string
  onChange: (value: string) => void
}) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <span className="w-14 shrink-0 text-sm text-muted-foreground">{label}</span>
      {options.length === 0 ? (
        <span className="text-sm text-muted-foreground">{emptyHint}</span>
      ) : (
        <span className="flex flex-wrap gap-2">
          {options.map((option) => (
            <Button
              key={option.id}
              size="sm"
              variant={option.id === value ? 'secondary' : 'outline'}
              aria-pressed={option.id === value}
              onClick={() => onChange(option.id === value ? '' : option.id)}
            >
              {option.name}
            </Button>
          ))}
        </span>
      )}
    </div>
  )
}

function dedup(items: readonly CatalogItem[]): CatalogItem[] {
  const seen = new Set<string>()
  const out: CatalogItem[] = []
  for (const item of items) {
    if (seen.has(item.series_id)) continue
    seen.add(item.series_id)
    out.push(item)
  }
  return out
}
