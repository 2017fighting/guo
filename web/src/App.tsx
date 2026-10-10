import { Route, Routes, useLocation } from 'react-router-dom'
import { AppShell, type NavKey } from '@/components/app-shell'
import { ThemeProvider } from '@/components/theme-provider'
import { BrowsePage } from '@/pages/browse'
import { SearchPage } from '@/pages/search'

// 路由表：每页一个 Route 块（后续 lane 在此追加，AppShell 保持唯一包裹）。
// active 顶栏/底栏高亮按路径推导；未匹配路径回落浏览页。
function activeNavKey(pathname: string): NavKey {
  if (pathname.startsWith('/search')) return 'search'
  return 'browse'
}

export default function App() {
  const pathname = useLocation().pathname
  return (
    <ThemeProvider>
      <AppShell active={activeNavKey(pathname)}>
        <Routes>
          <Route path="/" element={<BrowsePage />} />
          <Route path="/search" element={<SearchPage />} />
        </Routes>
      </AppShell>
    </ThemeProvider>
  )
}
