import { Route, Routes } from 'react-router-dom'
import { AppShell } from '@/components/app-shell'
import { ThemeProvider } from '@/components/theme-provider'
import { BrowsePage } from '@/pages/browse'

export default function App() {
  return (
    <ThemeProvider>
      <AppShell active="browse">
        <Routes>
          <Route path="/" element={<BrowsePage />} />
        </Routes>
      </AppShell>
    </ThemeProvider>
  )
}
