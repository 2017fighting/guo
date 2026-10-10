import { AppShell } from '@/components/app-shell'
import { ThemeProvider } from '@/components/theme-provider'
import { BrowsePage } from '@/pages/browse'

export default function App() {
  return (
    <ThemeProvider>
      <AppShell active="browse">
        <BrowsePage />
      </AppShell>
    </ThemeProvider>
  )
}
