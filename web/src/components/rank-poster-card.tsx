// 排行榜海报卡：2:3 封面 + rank 序号角标（前三名高亮）+ 标题/集数/热度，
// 整卡链接到 /drama/:series_id（详情路由属 lane #11，按约定链接）。
// 布局对齐 PosterCard（榜单条目字段与目录卡不同：rank/heat/episode_count）。

import { Link } from 'react-router-dom'
import { formatMetric } from '@/components/poster-card'
import type { RankingItem } from '@/types/rankings'

function rankBadgeClass(rank: number): string {
  if (rank <= 3) return 'bg-destructive text-white'
  return 'bg-primary/85 text-background'
}

function subParts(item: RankingItem): string[] {
  const parts: string[] = []
  if (item.episode_count) parts.push(`${item.episode_count} 集`)
  const metric = item.heat
    ? item.heat
    : item.play_count
      ? formatMetric(item.play_count, '播放')
      : item.score
        ? `评分 ${item.score}`
        : ''
  if (metric) parts.push(metric)
  return parts
}

export function RankPosterCard({ item }: { item: RankingItem }) {
  return (
    <Link
      to={`/drama/${item.series_id}`}
      className="group block overflow-hidden rounded-lg border bg-card transition-colors hover:border-ring"
    >
      <div className="relative aspect-[2/3] bg-muted">
        {item.cover ? (
          <img
            src={item.cover}
            alt={item.title}
            loading="lazy"
            className="h-full w-full object-cover"
            onError={(event) => {
              event.currentTarget.remove()
            }}
          />
        ) : (
          <div className="flex h-full items-center justify-center p-3 text-center text-sm font-semibold text-muted-foreground">
            {item.title}
          </div>
        )}
        {item.rank > 0 && (
          <span
            className={`absolute top-1.5 left-1.5 inline-flex size-6 items-center justify-center rounded-full text-xs font-bold tabular-nums shadow-sm ${rankBadgeClass(item.rank)}`}
            aria-label={`第 ${item.rank} 名`}
          >
            {item.rank}
          </span>
        )}
      </div>
      <div className="px-2.5 pt-2 pb-2.5">
        <div className="line-clamp-2 min-h-9 text-[13px] leading-snug font-medium">{item.title}</div>
        <div className="mt-1 flex flex-wrap items-center gap-x-1.5 gap-y-0.5 text-xs text-muted-foreground">
          {subParts(item).map((part, index) => (
            <span key={part} className="contents">
              {index > 0 && <span aria-hidden>·</span>}
              <span>{part}</span>
            </span>
          ))}
        </div>
      </div>
    </Link>
  )
}

// 榜单海报墙：栅格口径与 PosterWall 一致（2/3/5/6 列）。
export function RankPosterWall({ items }: { items: readonly RankingItem[] }) {
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 sm:gap-4 lg:grid-cols-5 xl:grid-cols-6">
      {items.map((item) => (
        <RankPosterCard key={item.series_id} item={item} />
      ))}
    </div>
  )
}
