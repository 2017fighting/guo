// 海报墙：响应式栅格（2/3/5/6 列，对齐 mockups/browse.html poster-grid）。
// hrefFor 提供时每张卡包一层路由 Link（搜索/榜单结果进详情页用）。

import { Link } from 'react-router-dom'
import { PosterCard } from '@/components/poster-card'
import { Skeleton } from '@/components/ui/skeleton'
import type { CatalogItem } from '@/types/catalog'

interface PosterWallProps {
  items: readonly CatalogItem[]
  hrefFor?: (item: CatalogItem) => string
}

export function PosterWall({ items, hrefFor }: PosterWallProps) {
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 sm:gap-4 lg:grid-cols-5 xl:grid-cols-6">
      {items.map((item) =>
        hrefFor ? (
          <Link
            key={item.series_id}
            to={hrefFor(item)}
            className="block rounded-lg outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            <PosterCard item={item} />
          </Link>
        ) : (
          <PosterCard key={item.series_id} item={item} />
        ),
      )}
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
