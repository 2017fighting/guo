// 应用外壳：顶部导航（浏览/排行榜/下载队列 + 搜索入口 + 主题切换）与
// 移动端底部 Tab。排行榜/下载/搜索页在后续工单接入路由时转正。

import { Clapperboard, Download, LayoutGrid, Moon, Search, Settings, Sun } from 'lucide-react'
import type { ReactNode } from 'react'
import { Button } from '@/components/ui/button'
import { useTheme } from '@/components/theme-provider'

export type NavKey = 'browse' | 'rankings' | 'downloads' | 'search' | 'settings'

interface NavItemSpec {
  key: NavKey
  label: string
  ready: boolean
}

const NAV_ITEMS: NavItemSpec[] = [
  { key: 'browse', label: '浏览', ready: true },
  { key: 'rankings', label: '排行榜', ready: false },
  { key: 'downloads', label: '下载队列', ready: false },
]

const TABBAR_ITEMS: NavItemSpec[] = [
  { key: 'browse', label: '浏览', ready: true },
  { key: 'search', label: '搜索', ready: false },
  { key: 'downloads', label: '下载', ready: false },
]

function TopNavItem({ spec, active }: { spec: NavItemSpec; active: boolean }) {
  if (!spec.ready) {
    return (
      <span
        className="inline-flex min-h-11 items-center rounded-lg px-3 text-muted-foreground opacity-60"
        title="即将上线"
      >
        {spec.label}
      </span>
    )
  }
  return (
    <span
      aria-current={active ? 'page' : undefined}
      className={`inline-flex min-h-11 items-center rounded-lg px-3 font-medium ${
        active ? 'bg-accent text-foreground' : 'text-muted-foreground hover:text-foreground'
      }`}
    >
      {spec.label}
    </span>
  )
}

export function AppShell({ active, children }: { active: NavKey; children: ReactNode }) {
  const { theme, toggle } = useTheme()
  return (
    <div className="flex min-h-dvh flex-col bg-background text-foreground">
      <header className="sticky top-0 z-40 border-b bg-background">
        <div className="mx-auto flex h-14 w-full max-w-7xl items-center gap-4 px-4">
          <a href="/" className="flex items-center gap-2 text-base font-bold tracking-wide">
            <Clapperboard className="size-5" aria-hidden />
            红果鉴·Web
          </a>
          <nav className="hidden items-center gap-1 md:flex" aria-label="主导航">
            {NAV_ITEMS.map((spec) => (
              <TopNavItem key={spec.key} spec={spec} active={spec.key === active} />
            ))}
          </nav>
          <div className="flex-1" />
          <Button
            variant="outline"
            size="sm"
            className="hidden md:inline-flex"
            aria-label="搜索"
            title="即将上线"
            disabled
          >
            <Search data-icon="inline-start" aria-hidden />
            搜索
          </Button>
          <Button
            variant="outline"
            size="icon"
            aria-label="切换深浅主题"
            onClick={toggle}
          >
            {theme === 'dark' ? <Sun aria-hidden /> : <Moon aria-hidden />}
          </Button>
          <Button
            variant={active === 'settings' ? 'default' : 'outline'}
            size="icon"
            aria-label="设置"
            aria-current={active === 'settings' ? 'page' : undefined}
            asChild
          >
            <a href="/settings">
              <Settings aria-hidden />
            </a>
          </Button>
        </div>
      </header>
      <main className="mx-auto w-full max-w-7xl flex-1 px-4 pt-4 pb-24 md:pb-8">{children}</main>
      <nav
        className="fixed inset-x-0 bottom-0 z-40 flex border-t bg-background md:hidden"
        aria-label="底部导航"
      >
        {TABBAR_ITEMS.map((spec) => {
          const activeTab = spec.key === active
          const Icon = spec.key === 'search' ? Search : spec.key === 'downloads' ? Download : LayoutGrid
          return (
            <span
              key={spec.key}
              aria-current={activeTab ? 'page' : undefined}
              title={spec.ready ? spec.label : '即将上线'}
              className={`flex min-h-14 flex-1 flex-col items-center justify-center gap-0.5 text-xs ${
                activeTab ? 'font-medium text-foreground' : 'text-muted-foreground'
              } ${spec.ready ? '' : 'opacity-60'}`}
            >
              <Icon className="size-5" aria-hidden />
              {spec.label}
            </span>
          )
        })}
      </nav>
    </div>
  )
}
