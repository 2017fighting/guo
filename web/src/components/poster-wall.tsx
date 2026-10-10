// 海报墙：响应式栅格（2/3/5/6 列，对齐 mockups/browse.html poster-grid）。
// 卡片可点击进入详情页（/drama/:series_id）——详情/搜索/榜单通用行为。

import { Link } from 'react-router-dom'
import { PosterCard } from '@/components/poster-card'
import { Skeleton } from '@/components/ui/skeleton'
import type { CatalogItem } from '@/types/catalog'

export function PosterWall({ items }: { items: readonly CatalogItem[] }) {
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 sm:gap-4 lg:grid-cols-5 xl:grid-cols-6">
      {items.map((item) => (
        <Link key={item.series_id} to={`/drama/${item.series_id}`} className="rounded-lg focus-visible:outline-2">
          <PosterCard item={item} />
        </Link>
      ))}
    </div>
  )
}

export function PosterWallSkeleton({ count = 12 }: { count?: number }) {
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 sm:gap-4 lg:grid-cols-5 xl:grid-cols-6">
      {Array.from({ length: count }, (_, index) => (
        <div key={index} className="overflow-hidden rounded-lg border bg-card">
          <Skeleton className="aspect-[2/3] rounded-none" />
          <div className="space-y-2 px-2.5 py-2">
            <Skeleton className="h-4 w-full" />
            <Skeleton className="h-3 w-2/3" />
          </div>
        </div>
      ))}
    </div>
  )
}
