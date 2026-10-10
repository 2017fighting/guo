// 选集分组 Tab 条（1–50 / 51–100 / …）：单行横向滚动（隐藏滚动条，移动端
// 滑动切换），激活分组变化时把激活 tab scrollIntoView 到就近可见位置，
// 保证多分组剧集在窄屏也能滑到最后一组。详情页与播放页共用。

import { useEffect, useRef } from 'react'
import { Button } from '@/components/ui/button'
import type { EpisodeGroup } from '@/lib/episode-groups'

export function EpisodeGroupTabs({
  groups,
  active,
  onSelect,
}: {
  groups: readonly EpisodeGroup[]
  active: EpisodeGroup | undefined
  onSelect: (start: number) => void
}) {
  const tabRefs = useRef(new Map<number, HTMLButtonElement>())

  // 激活分组变化（点击切组/播放集换组）→ 滚动条带到激活 tab（nearest，不打扰页面滚动）
  useEffect(() => {
    if (!active) return
    tabRefs.current.get(active.start)?.scrollIntoView({ inline: 'nearest', block: 'nearest' })
  }, [active])

  if (groups.length === 0) return null

  return (
    <div
      role="tablist"
      aria-label="选集分组"
      className="flex min-w-0 gap-2 overflow-x-auto overflow-y-hidden pb-0.5 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
    >
      {groups.map((g) => (
        <Button
          key={g.start}
          ref={(el) => {
            if (el) tabRefs.current.set(g.start, el)
            else tabRefs.current.delete(g.start)
          }}
          role="tab"
          aria-selected={g.start === active?.start}
          size="sm"
          variant={g.start === active?.start ? 'secondary' : 'outline'}
          onClick={() => onSelect(g.start)}
        >
          {g.start === g.end ? `${g.start}` : `${g.start}–${g.end}`}
        </Button>
      ))}
    </div>
  )
}
