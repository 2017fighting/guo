// 海报卡：2:3 封面 + 标题/集数/状态/热度 + VIP 角标。
// 详情页路由在 #11 接入后由外层包 Link，卡片保持纯展示、可复用（榜单/搜索）。

import { Badge } from '@/components/ui/badge'
import type { CatalogItem } from '@/types/catalog'

function subParts(item: CatalogItem): string[] {
  const parts: string[] = []
  if (item.episode_count) parts.push(`${item.episode_count} 集`)
  if (item.status) parts.push(item.status)
  // landpage 现网卡片常缺热度字段（实测 89 条 0 命中），退回播放量展示
  const metric = item.heat ? formatMetric(item.heat, '热度') : item.play_count ? formatMetric(item.play_count, '播放') : ''
  if (metric) parts.push(metric)
  return parts
}

// 热度/播放量展示：纯数值文本（实测 landpage play_count 恒为数字）转亿/万口径；
// 已带单位/文字的文本（如「4868万热度」）原样展示。
export function formatMetric(text: string, suffix: string): string {
  if (!/^[\d.,+\s]+$/.test(text.trim())) return text
  const value = Number.parseFloat(text.replace(/[,，\s+]/g, ''))
  if (Number.isNaN(value) || value < 0) return text
  if (value >= 1e8) return `${round(value / 1e8)}亿${suffix}`
  if (value >= 1e4) return `${round(value / 1e4)}万${suffix}`
  return `${round(value)}${suffix}`
}

function round(value: number): string {
  const text = value >= 100 ? value.toFixed(0) : value.toFixed(1).replace(/\.0$/, '')
  return text
}

export function PosterCard({ item }: { item: CatalogItem }) {
  return (
    <div className="group overflow-hidden rounded-lg border bg-card transition-colors hover:border-ring">
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
        {item.vip && (
          <Badge className="absolute top-1.5 right-1.5 bg-destructive text-white">VIP</Badge>
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
    </div>
  )
}
