// 播放页类型 —— 与后端 internal/server/stream.go streamPayload、
// danmaku.go danmakuPayload 一一对应。

export interface StreamQuality {
  id: number
  label: string
}

export interface StreamInfo {
  proxy_url: string
  line: string // ''(自动解析出) | web | app | fallback
  line_label: string
  qualities: StreamQuality[]
  quality: number // 生效画质（0 = 自动）
  duration_ms: number
  vertical: boolean
  cenc: boolean
  hls: boolean
}

export interface DanmakuItem {
  id?: string
  offset_ms: number
  text: string
}

export interface DanmakuWindow {
  items: DanmakuItem[]
  next_ms: number
}

// 红果弹幕 30s 窗口游标 + 8s 滚动生命周期（guoapp danmaku 口径，与 ASS 导出一致）。
export const DANMAKU_WINDOW_MS = 30_000
export const DANMAKU_LIFETIME_MS = 8_000

export const LINE_OPTIONS: ReadonlyArray<{ value: string; label: string }> = [
  { value: '', label: '自动' },
  { value: 'web', label: '官网' },
  { value: 'app', label: 'App' },
  { value: 'fallback', label: '兜底' },
]
