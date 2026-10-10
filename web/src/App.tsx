import { Route, Routes, useLocation } from 'react-router-dom'
import { AppShell, type NavKey } from '@/components/app-shell'
import { ThemeProvider } from '@/components/theme-provider'
import { BrowsePage } from '@/pages/browse'
import { SearchPage } from '@/pages/search'
import { SettingsPage } from '@/pages/settings'

// 路由表（后续 lane 各加一行 <Route>）；导航高亮按当前路径推导。
const NAV_BY_PATH: Record<string, NavKey> = {
  '/': 'browse',
  '/search': 'search',
  '/settings': 'settings',
}

function ShellRoutes() {
  const { pathname } = useLocation()
  const active = NAV_BY_PATH[pathname] ?? 'browse'
  return (
    <AppShell active={active}>
      <Routes>
        <Route path="/" element={<BrowsePage />} />
        <Route path="/search" element={<SearchPage />} />
        <Route path="/settings" element={<SettingsPage />} />
        {/* 未接入路由（如 /drama/:id 等）暂回落浏览页，对应 lane 合入后自然接上 */}
        <Route path="*" element={<BrowsePage />} />
      </Routes>
    </AppShell>
  )
}

export default function App() {
  return (
    <ThemeProvider>
      <ShellRoutes />
    </ThemeProvider>
  )
}
