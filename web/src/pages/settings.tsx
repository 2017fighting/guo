// 设置页：弹幕导出开关 / 下载并发 / Jellyfin 联动（保存时验活）/
// 出站代理（预留，只存不生效）。无 mockup——按 ui-contract 令牌的最小表单。
// 对接 GET/PUT /api/v1/settings；保存失败展示后端人话 message+hint。

import { useEffect, useState } from 'react'
import { CheckCircle2, CircleAlert, LoaderCircle } from 'lucide-react'
import { api, apiPut, ApiError } from '@/lib/api'
import type { Settings } from '@/types/settings'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'

const CONCURRENCY_OPTIONS = [1, 2, 3, 4, 5, 6]

function concurrencyLabel(n: number): string {
  if (n === 2) return '2（默认）'
  if (n === 1) return '1（最稳）'
  if (n === 6) return '6（上限）'
  return String(n)
}

type Phase = 'loading' | 'error' | 'ready'

interface SaveResult {
  ok: boolean
  message: string
  hint?: string
}

export function SettingsPage() {
  const [phase, setPhase] = useState<Phase>('loading')
  const [loadError, setLoadError] = useState<ApiError | null>(null)
  const [form, setForm] = useState<Settings | null>(null)
  const [saving, setSaving] = useState(false)
  const [saveResult, setSaveResult] = useState<SaveResult | null>(null)

  useEffect(() => {
    let cancelled = false
    api<Settings>('/api/v1/settings')
      .then((s) => {
        if (!cancelled) {
          setForm(s)
          setPhase('ready')
        }
      })
      .catch((err: ApiError) => {
        if (!cancelled) {
          setLoadError(err)
          setPhase('error')
        }
      })
    return () => {
      cancelled = true
    }
  }, [])

  const save = async () => {
    if (!form || saving) return
    setSaving(true)
    setSaveResult(null)
    try {
      const saved = await apiPut<Settings>('/api/v1/settings', form)
      setForm(saved) // 回显即服务端权威状态（api_key 恒空、api_key_set 最新）
      const jfNote = saved.jellyfin.url ? '，Jellyfin 验活通过' : ''
      setSaveResult({ ok: true, message: `设置已保存${jfNote}` })
    } catch (err) {
      const e = err instanceof ApiError ? err : null
      setSaveResult({
        ok: false,
        message: e?.message ?? '保存失败，请稍后重试',
        hint: e?.hint,
      })
    } finally {
      setSaving(false)
    }
  }

  if (phase === 'loading') {
    return <p className="py-16 text-center text-muted-foreground">设置加载中…</p>
  }
  if (phase === 'error' || !form) {
    return (
      <div className="mx-auto max-w-2xl space-y-3 py-16 text-center">
        <p className="font-medium">设置加载失败</p>
        <p className="text-sm text-muted-foreground">{loadError?.message ?? '未知错误'}</p>
        <p className="text-xs text-muted-foreground">{loadError?.hint}</p>
      </div>
    )
  }

  return (
    <div className="mx-auto max-w-2xl space-y-4 pb-8">
      <header className="space-y-1">
        <h1 className="font-heading text-xl font-bold">设置</h1>
        <p className="text-sm text-muted-foreground">
          保存后立即生效：并发在下个任务开始时应用，字幕开关影响后续分集导出。
        </p>
      </header>

      {/* 弹幕导出 */}
      <Card>
        <CardHeader>
          <CardTitle>弹幕导出</CardTitle>
          <CardDescription>
            下载完成时把弹幕转成 ASS 字幕（与视频同名，Jellyfin 可直接加载）。
          </CardDescription>
        </CardHeader>
        <CardContent>
          <label className="flex items-center justify-between gap-4">
            <span className="text-sm">导出弹幕</span>
            <Switch
              checked={form.ass_export}
              onCheckedChange={(v) => setForm({ ...form, ass_export: v })}
              aria-label="弹幕导出开关"
            />
          </label>
        </CardContent>
      </Card>

      {/* 下载并发 */}
      <Card>
        <CardHeader>
          <CardTitle>下载并发</CardTitle>
          <CardDescription>同时下载的剧集数（1–6）。改动对下个开始的任务生效，不打断进行中的任务。</CardDescription>
        </CardHeader>
        <CardContent>
          <label className="flex items-center justify-between gap-4">
            <span className="text-sm">并发剧集数</span>
            <Select
              value={String(form.concurrency)}
              onValueChange={(v) => setForm({ ...form, concurrency: Number(v) })}
            >
              <SelectTrigger className="w-36" aria-label="下载并发">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {CONCURRENCY_OPTIONS.map((n) => (
                  <SelectItem key={n} value={String(n)}>
                    {concurrencyLabel(n)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </label>
        </CardContent>
      </Card>

      {/* Jellyfin 联动 */}
      <Card>
        <CardHeader>
          <CardTitle>Jellyfin 联动</CardTitle>
          <CardDescription>
            保存时调用 Jellyfin /System/Info 验活，通过才会保存；整剧下载完成后自动触发库刷新。
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="space-y-1.5">
            <label htmlFor="jf-url" className="block text-sm">
              服务器地址
            </label>
            <Input
              id="jf-url"
              placeholder="http://jellyfin:8096（留空关闭联动）"
              value={form.jellyfin.url}
              onChange={(e) =>
                setForm({ ...form, jellyfin: { ...form.jellyfin, url: e.target.value } })
              }
            />
          </div>
          <div className="space-y-1.5">
            <label htmlFor="jf-key" className="block text-sm">
              API Key
            </label>
            <Input
              id="jf-key"
              type="password"
              autoComplete="off"
              placeholder={
                form.jellyfin.api_key_set ? '已配置（不回显），留空则沿用' : '在 Jellyfin 控制台生成'
              }
              value={form.jellyfin.api_key}
              onChange={(e) =>
                setForm({ ...form, jellyfin: { ...form.jellyfin, api_key: e.target.value } })
              }
            />
          </div>
        </CardContent>
      </Card>

      {/* 出站代理（预留） */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            出站代理
            <Badge variant="secondary">预留</Badge>
          </CardTitle>
          <CardDescription>配置位先行保存，当前版本不会对任何出站请求生效。</CardDescription>
        </CardHeader>
        <CardContent>
          <label className="flex items-center justify-between gap-4">
            <span className="text-sm">代理地址</span>
            <Input
              className="max-w-72"
              placeholder="暂不生效"
              value={form.proxy.proxy_url}
              onChange={(e) => setForm({ ...form, proxy: { ...form.proxy, proxy_url: e.target.value } })}
              disabled
            />
          </label>
        </CardContent>
      </Card>

      {/* 保存 + 反馈 */}
      <div className="space-y-2">
        <div className="flex items-center gap-3">
          <Button onClick={save} disabled={saving}>
            {saving ? <LoaderCircle data-icon="inline-start" className="animate-spin" aria-hidden /> : null}
            {saving ? '保存中…（含 Jellyfin 验活）' : '保存设置'}
          </Button>
          {saveResult ? (
            <p
              className={`flex items-center gap-1.5 text-sm ${saveResult.ok ? 'text-foreground' : 'text-destructive'}`}
              role="status"
            >
              {saveResult.ok ? (
                <CheckCircle2 className="size-4" aria-hidden />
              ) : (
                <CircleAlert className="size-4" aria-hidden />
              )}
              {saveResult.message}
            </p>
          ) : null}
        </div>
        {saveResult && !saveResult.ok && saveResult.hint ? (
          <p className="text-xs text-muted-foreground">{saveResult.hint}</p>
        ) : null}
      </div>
    </div>
  )
}
