// 海报卡：2:3 封面 + 标题/集数/状态/热度 + VIP 角标。
// 详情页路由在 #11 接入后由外层包 Link，卡片保持纯展示、可复用（榜单/搜索）。

import { Badge } from '@/components/ui/badge'
import type { CatalogItem } from '@/types/catalog'

function subParts(item: CatalogItem): string[] {
  const parts: string[] = []
  if (item.episode_count) parts.push(`${item.episode_count} 集`)
  if (item.status) parts.push(item.status)
  if (item.heat) parts.push(formatHeat(item.heat))
  return parts
}

// 纯数字热度（hot_score_data.score 数值文本）转亿/万展示；已带单位文本原样展示。
export function formatHeat(heat: string): string {
  if (/[^\d.,+\s]/.test(heat)) return heat // 已含单位/文字
  const value = Number.parseFloat(heat.replace(/[,，\s+]/g, ''))
  if (Number.isNaN(value) || value < 0) return heat
  if (value >= 1e8) return `${round(value / 1e8)}亿热度`
  if (value >= 1e4) return `${round(value / 1e4)}万热度`
  return `${round(value)}热度`
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
