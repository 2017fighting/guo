// 搜索页：联想下拉（停顿 300ms 拉联想、键盘 ↑↓+Enter、Esc 关闭、剧名联想直达详情）
// + 最近搜索 chips（localStorage ≤20，可单删/清空）+ 结果海报墙（与浏览页同款卡）。
// 交互对齐 mockups/search.html；防抖在前端做（后端不做）。

import { useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { Search, SearchX, X } from 'lucide-react'
import { api, ApiError } from '@/lib/api'
import {
  addRecentSearch,
  clearRecentSearches,
  loadRecentSearches,
  removeRecentSearch,
} from '@/lib/recent-searches'
import type { SearchResponse, SuggestItem } from '@/types/search'
import { PosterWall, PosterWallSkeleton } from '@/components/poster-wall'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'

const SUGGEST_DEBOUNCE_MS = 300

/** 联想 word_type → 展示标签（白名单 5 值；白名单外后端已置空）。 */
const SUGGEST_TYPE_LABELS: Record<string, string> = {
  short_play_name: '剧名',
  short_play_category: '分类',
  common_query: '联想',
  actor_name: '演员',
  short_play_actor: '演员',
}

type Phase = 'idle' | 'loading' | 'error' | 'ready'

/** 联想名里命中输入的部分加粗（大小写不敏感，找不到就不加粗）。 */
function HighlightName({ name, query }: { name: string; query: string }) {
  const needle = query.trim().toLowerCase()
  const idx = needle ? name.toLowerCase().indexOf(needle) : -1
  if (idx < 0) return <>{name}</>
  return (
    <>
      {name.slice(0, idx)}
      <strong className="font-semibold text-foreground">{name.slice(idx, idx + needle.length)}</strong>
      {name.slice(idx + needle.length)}
    </>
  )
}

export function SearchPage() {
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()

  const [input, setInput] = useState(() => (searchParams.get('q') ?? '').trim())
  const [suggests, setSuggests] = useState<SuggestItem[]>([])
  const [suggestOpen, setSuggestOpen] = useState(false)
  const [activeIndex, setActiveIndex] = useState(-1)

  const [phase, setPhase] = useState<Phase>('idle')
  const [result, setResult] = useState<SearchResponse | null>(null)
  const [lastQuery, setLastQuery] = useState('')
  const [error, setError] = useState<{ message: string; hint: string } | null>(null)
  const [recents, setRecents] = useState<string[]>(() => loadRecentSearches())

  // 生成号：丢弃过期联想响应（快速输入时只认最后一次，guoapp search_input 同款）
  const suggestSeqRef = useRef(0)
  // 当前执行中的搜索词（重试用，避免过期响应覆盖新搜索）
  const searchSeqRef = useRef(0)

  const runSearch = useCallback(
    async (rawQuery: string) => {
      const q = rawQuery.trim()
      if (!q) return
      const seq = ++searchSeqRef.current
      setInput(q)
      setSuggestOpen(false)
      setActiveIndex(-1)
      setPhase('loading')
      setError(null)
      setLastQuery(q)
      setSearchParams({ q }, { replace: true })
      try {
        const resp = await api<SearchResponse>('/api/v1/search', { q })
        if (seq !== searchSeqRef.current) return
        setResult(resp)
        setPhase('ready')
        setRecents(addRecentSearch(q))
      } catch (err) {
        if (seq !== searchSeqRef.current) return
        if (err instanceof ApiError) {
          setError({ message: err.message, hint: err.hint })
        } else {
          setError({ message: '网络不给力，搜索失败', hint: '请检查服务是否在运行，稍后重试' })
        }
        setPhase('error')
      }
    },
    [setSearchParams],
  )

  // 深链初始搜索（/search?q=…）
  useEffect(() => {
    const q = (searchParams.get('q') ?? '').trim()
    if (q) void runSearch(q)
    // 仅首次挂载执行；后续 URL 变化由本页自己的搜索驱动
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // 联想防抖 300ms：输入停顿后拉联想；清空输入即收起
  useEffect(() => {
    const q = input.trim()
    if (!q) {
      suggestSeqRef.current++
      setSuggests([])
      setSuggestOpen(false)
      setActiveIndex(-1)
      return
    }
    const seq = ++suggestSeqRef.current
    const timer = setTimeout(() => {
      api<{ items: SuggestItem[] }>('/api/v1/search/suggest', { q })
        .then((resp) => {
          if (seq !== suggestSeqRef.current) return
          setSuggests(resp.items)
          setSuggestOpen(resp.items.length > 0)
          setActiveIndex(-1)
        })
        .catch(() => {
          // 联想拉不到不阻塞输入；静默收起即可（回车仍可全量搜索）
          if (seq !== suggestSeqRef.current) return
          setSuggestOpen(false)
        })
    }, SUGGEST_DEBOUNCE_MS)
    return () => clearTimeout(timer)
  }, [input])

  const pickSuggest = useCallback(
    (item: SuggestItem) => {
      if (item.series_id) {
        // 剧名联想直达详情（详情路由由 #11 接入）
        setRecents(addRecentSearch(item.name))
        navigate(`/drama/${item.series_id}`)
        return
      }
      void runSearch(item.name)
    },
    [navigate, runSearch],
  )

  const onKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    // 中文输入法组词中的按键（回车确认候选等）不触发导航/搜索
    if (event.nativeEvent.isComposing) return
    if (event.key === 'ArrowDown' && suggestOpen && suggests.length > 0) {
      event.preventDefault()
      setActiveIndex((i) => (i + 1) % suggests.length)
    } else if (event.key === 'ArrowUp' && suggestOpen && suggests.length > 0) {
      event.preventDefault()
      setActiveIndex((i) => (i - 1 + suggests.length) % suggests.length)
    } else if (event.key === 'Enter') {
      event.preventDefault()
      if (suggestOpen && activeIndex >= 0 && suggests[activeIndex]) {
        pickSuggest(suggests[activeIndex])
      } else {
        void runSearch(input)
      }
    } else if (event.key === 'Escape') {
      setSuggestOpen(false)
      setActiveIndex(-1)
    }
  }

  const showRecents = recents.length > 0

  return (
    <div className="mx-auto w-full max-w-3xl">
      {/* 搜索框 + 联想 */}
      <div className="relative mb-3">
        <label className="sr-only" htmlFor="search-q">
          搜索剧名、演员或题材
        </label>
        <Search
          className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
          aria-hidden
        />
        <input
          id="search-q"
          className="h-11 w-full rounded-lg border bg-background pl-10 pr-24 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
          style={{ paddingRight: '6rem' }}
          placeholder="搜索剧名、演员…"
          autoComplete="off"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={onKeyDown}
          onBlur={() => {
            // 延迟一拍让选项的 mousedown 先行（选项用 onMouseDown 选择）
            setTimeout(() => setSuggestOpen(false), 120)
          }}
        />
        <Button
          size="sm"
          className="absolute top-1/2 right-1.5 -translate-y-1/2"
          onClick={() => void runSearch(input)}
        >
          搜索
        </Button>
        {suggestOpen && suggests.length > 0 && (
          <div
            className="absolute inset-x-0 top-full z-50 mt-1 overflow-hidden rounded-lg border bg-popover shadow-md"
            role="listbox"
            aria-label="搜索联想"
          >
            {suggests.map((item, index) => (
              <button
                key={`${item.name}-${index}`}
                type="button"
                role="option"
                aria-selected={index === activeIndex}
                // mousedown 选择并阻止失焦，避免 blur 先收起下拉
                onMouseDown={(e) => {
                  e.preventDefault()
                  pickSuggest(item)
                }}
                className={`flex w-full items-center justify-between gap-3 px-4 py-2.5 text-left text-sm ${
                  index === activeIndex ? 'bg-accent text-accent-foreground' : 'hover:bg-accent/50'
                }`}
              >
                <span className="truncate">
                  <HighlightName name={item.name} query={input} />
                </span>
                {SUGGEST_TYPE_LABELS[item.type] && (
                  <span className="shrink-0 text-xs text-muted-foreground">
                    {SUGGEST_TYPE_LABELS[item.type]}
                  </span>
                )}
              </button>
            ))}
          </div>
        )}
      </div>

      {/* 最近搜索 */}
      {showRecents && (
        <div className="mb-4 flex flex-wrap items-center gap-2">
          <span className="text-sm text-muted-foreground">最近搜索</span>
          {recents.map((q) => (
            <span
              key={q}
              className="inline-flex min-h-8 items-center gap-1 rounded-lg border px-2.5 text-sm text-muted-foreground"
            >
              <button type="button" className="hover:text-foreground" onClick={() => void runSearch(q)}>
                {q}
              </button>
              <button
                type="button"
                aria-label={`删除最近搜索 ${q}`}
                className="rounded p-0.5 hover:text-foreground"
                onClick={() => setRecents(removeRecentSearch(q))}
              >
                <X className="size-3.5" aria-hidden />
              </button>
            </span>
          ))}
          <button
            type="button"
            className="px-1 text-sm text-muted-foreground hover:text-foreground"
            onClick={() => setRecents(clearRecentSearches())}
          >
            清除
          </button>
        </div>
      )}

      {/* 结果区 */}
      {phase === 'loading' && <PosterWallSkeleton count={12} />}

      {phase === 'error' && error && (
        <Card>
          <CardContent className="flex flex-col items-start gap-3 pt-0">
            <div>
              <p className="font-medium">{error.message}</p>
              <p className="mt-1 text-sm text-muted-foreground">{error.hint}</p>
            </div>
            {lastQuery && (
              <Button variant="outline" size="sm" onClick={() => void runSearch(lastQuery)}>
                重试「{lastQuery}」
              </Button>
            )}
          </CardContent>
        </Card>
      )}

      {phase === 'ready' && result && (
        <>
          <h2 className="mb-1 flex flex-wrap items-baseline gap-2 text-lg font-semibold">
            搜索结果
            <span className="text-sm font-normal text-muted-foreground">共 {result.items.length} 部</span>
            {result.limited && (
              <span className="text-sm font-normal text-muted-foreground">（官网仅返回部分匹配）</span>
            )}
          </h2>
          {result.warnings.map((warning) => (
            <p key={warning} className="mb-1 text-sm text-muted-foreground">
              {warning}
            </p>
          ))}
          {result.items.length > 0 ? (
            <div className="mt-3">
              <PosterWall items={result.items} hrefFor={(item) => `/drama/${item.series_id}`} />
            </div>
          ) : (
            <EmptyState
              title="没有找到相关剧集"
              hints={['换个更短的关键词试试', '检查有没有错别字', '也可以用演员名、题材词搜索']}
            />
          )}
        </>
      )}

      {phase === 'idle' && (
        <EmptyState
          title="搜索红果短剧"
          hints={[
            '输入剧名、演员或题材，停顿一下出联想',
            '联想里带「剧名」的可直达详情',
            '回车或点「搜索」看结果海报墙',
          ]}
        />
      )}
    </div>
  )
}

function EmptyState({ title, hints }: { title: string; hints: readonly string[] }) {
  return (
    <Card>
      <CardContent className="flex flex-col items-center gap-2 py-10 text-center">
        <SearchX className="size-8 text-muted-foreground" aria-hidden />
        <p className="font-medium">{title}</p>
        <ul className="space-y-1 text-sm text-muted-foreground">
          {hints.map((hint) => (
            <li key={hint}>{hint}</li>
          ))}
        </ul>
      </CardContent>
    </Card>
  )
}
