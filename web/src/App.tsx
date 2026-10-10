import { AppShell } from '@/components/app-shell'
import { ThemeProvider } from '@/components/theme-provider'
import { BrowsePage } from '@/pages/browse'
import { SettingsPage } from '@/pages/settings'

// 路径路由（后续 lane 各加一个分支即可）：/settings → 设置页，其余 → 浏览。
function route(pathname: string): 'settings' | 'browse' {
  if (pathname === '/settings' || pathname.startsWith('/settings/')) return 'settings'
  return 'browse'
}

export default function App() {
  const page = route(window.location.pathname)
  return (
    <ThemeProvider>
      {page === 'settings' ? (
        <AppShell active="settings">
          <SettingsPage />
        </AppShell>
      ) : (
        <AppShell active="browse">
          <BrowsePage />
        </AppShell>
      )}
    </ThemeProvider>
  )
}
