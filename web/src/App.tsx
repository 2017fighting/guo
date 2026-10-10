import { Route, Routes, useLocation } from 'react-router-dom'
import { AppShell, type NavKey } from '@/components/app-shell'
import { ThemeProvider } from '@/components/theme-provider'
import { BrowsePage } from '@/pages/browse'
import { DownloadsPage } from '@/pages/downloads'
import { DramaPage } from '@/pages/detail'
import { PlayPage } from '@/pages/play'
import { SettingsPage } from '@/pages/settings'

// 路由表（后续 lane 各加一行 <Route>）；导航高亮按当前路径推导。
const NAV_BY_PATH: Record<string, NavKey> = {
  '/': 'browse',
  '/downloads': 'downloads',
  '/settings': 'settings',
}

function ShellRoutes() {
  const { pathname } = useLocation()
  const active = NAV_BY_PATH[pathname] ?? 'browse'
  return (
    <AppShell active={active}>
      <Routes>
        <Route path="/" element={<BrowsePage />} />
        <Route path="/drama/:seriesID" element={<DramaPage />} />
        <Route path="/downloads" element={<DownloadsPage />} />
        <Route path="/play/:seriesID/:vid" element={<PlayPage />} />
        <Route path="/settings" element={<SettingsPage />} />
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
