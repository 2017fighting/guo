// 浏览页客户端排序/过滤 —— 语义逐条对齐 guoapp lib/catalog_sort.dart
//（guoapp-reference §2.2：catalogMetric 解析、normalizedSearchText 名称序、
// 降序指标 null 恒排末尾；名称序按 spec §4 名称/季号——同剧按季号升序，
// 其余 tie-break 用源位置）。

import type { CatalogFilters, CatalogItem } from '@/types/catalog'

export type SortKey = 'default' | 'heat' | 'date' | 'views' | 'name'

export const SORT_OPTIONS: ReadonlyArray<{ key: SortKey; label: string }> = [
  { key: 'default', label: '默认排序' },
  { key: 'heat', label: '热度最高' },
  { key: 'date', label: '最近上线' },
  { key: 'views', label: '播放量最多' },
  { key: 'name', label: '名称' },
]

// 热度/播放量文本解析（catalog_sort.dart:78 catalogMetric，逐字符照抄）：
// 支持 1.2亿 / 856万 / 6.3w / 6.3k / 2m / 1b / 裸数字，容忍 + 尾缀与
// 次播放/热度 等量词后缀；全角逗号/空格先剔除。解析失败 → null。
const METRIC_RE = /^(\d+(?:\.\d+)?)([亿万千wkmb]?)(?:\+)?(?:次播放|次观看|人看过|热度|播放|观看|次)?\+?$/

export function catalogMetric(text: string): number | null {
  const cleaned = text.toLowerCase().replace(/[,，\s]/g, '')
  const match = METRIC_RE.exec(cleaned)
  if (!match) return null
  const value = Number.parseFloat(match[1]!)
  const unit = match[2] ?? ''
  const mult =
    unit === '亿'
      ? 1e8
      : unit === '万' || unit === 'w'
        ? 1e4
        : unit === '千' || unit === 'k'
          ? 1e3
          : unit === 'm'
            ? 1e6
            : unit === 'b'
              ? 1e9
              : 1
  return value * mult
}

// 名称排序归一（catalog_sort.dart:54）：全角 ASCII（0xFF01-0xFF5E）转半角、
// 小写、去空白与 CJK/西文标点。
export function normalizedSearchText(text: string): string {
  let out = ''
  for (const ch of text) {
    const code = ch.codePointAt(0)!
    out += code >= 0xff01 && code <= 0xff5e ? String.fromCharCode(code - 0xfee0) : ch
  }
  return out.toLowerCase().replace(/[\s\p{P}\p{S}]+/gu, '')
}

function descendingByMetric(pick: (item: CatalogItem) => string) {
  return (a: CatalogItem, b: CatalogItem) => {
    const left = catalogMetric(pick(a))
    const right = catalogMetric(pick(b))
    if (left === null && right === null) return 0
    if (left === null) return 1 // null 恒排非 null 之后
    if (right === null) return -1
    return right - left
  }
}

// 季号后缀识别：语义移植自 internal/hongguo/search.go seasonSuffix/
// parseSeasonNumber——仅识别标题尾「第N季/第N部」（中文数字按十/百权位累加，
// 阿拉伯数字直转；季号 1–9999）；非结尾后缀或解析失败不识别。
const SEASON_NUMERAL = new Set(['零', '〇', '一', '二', '两', '三', '四', '五', '六', '七', '八', '九', '十', '百'])

const CHINESE_DIGIT: Record<string, number> = {
  一: 1,
  二: 2,
  两: 2,
  三: 3,
  四: 4,
  五: 5,
  六: 6,
  七: 7,
  八: 8,
  九: 9,
}

function parseSeasonNumber(seg: string): number | null {
  if (seg === '') return null
  if (/^\d+$/.test(seg)) return Number.parseInt(seg, 10)
  let total = 0
  let cur = 0
  for (const ch of seg) {
    if (ch === '零' || ch === '〇') continue
    if (ch === '十') {
      total += Math.max(cur, 1) * 10
      cur = 0
      continue
    }
    if (ch === '百') {
      total += Math.max(cur, 1) * 100
      cur = 0
      continue
    }
    const d = CHINESE_DIGIT[ch]
    if (d === undefined) return null
    cur = d
  }
  total += cur
  if (total <= 0 || total > 9999) return null
  return total
}

interface SeasonSuffix {
  base: string
  unit: string
  num: number
}

function seasonSuffix(title: string): SeasonSuffix | null {
  const runes = Array.from(title.trim())
  if (runes.length < 3) return null
  const tail = runes[runes.length - 1]!
  if (tail !== '季' && tail !== '部') return null
  for (let i = runes.length - 2; i >= 0; i--) {
    const r = runes[i]!
    if (r === '第') {
      const num = parseSeasonNumber(runes.slice(i + 1, runes.length - 1).join(''))
      const base = runes.slice(0, i).join('').trim()
      if (num === null || base === '') break
      return { base, unit: tail, num }
    }
    if (!SEASON_NUMERAL.has(r) && (r < '0' || r > '9')) break
  }
  return null
}

// compareByName 名称序（spec §4 名称/季号）：主键用去季号后缀的归一基名，
// 让同剧各季在名称序里相邻；基名与单位一致时按季号数值升序（第2季 < 第10季，
// 中文/阿拉伯数字同口径），其余交回 sort 稳定性保源位置。
function compareByName(a: CatalogItem, b: CatalogItem): number {
  const sa = seasonSuffix(a.title)
  const sb = seasonSuffix(b.title)
  const cmp = normalizedSearchText(sa ? sa.base : a.title).localeCompare(
    normalizedSearchText(sb ? sb.base : b.title),
    'zh',
  )
  if (cmp !== 0) return cmp
  if (sa && sb && sa.base === sb.base && sa.unit === sb.unit) return sa.num - sb.num
  return 0
}

// sortCatalogItems 排序副本（源顺序 tie-break 依赖 Array#sort 稳定性）。
export function sortCatalogItems(items: readonly CatalogItem[], sort: SortKey): CatalogItem[] {
  const out = [...items]
  switch (sort) {
    case 'heat':
      out.sort(descendingByMetric((item) => item.heat))
      break
    case 'views':
      out.sort(descendingByMetric((item) => item.play_count))
      break
    case 'date':
      out.sort((a, b) => (b.online_date || '').localeCompare(a.online_date || ''))
      break
    case 'name':
      out.sort(compareByName)
      break
  }
  return out
}

export interface CatalogFilterState {
  theme: string
  status: string
  onlineTime: string
}

export const EMPTY_FILTERS: CatalogFilterState = { theme: '', status: '', onlineTime: '' }

const STATUS_LABEL: Record<string, CatalogItem['status']> = {
  ongoing: '连载',
  finished: '完结',
}

function withinOnlineWindow(date: string, window: string, today: Date): boolean {
  if (!date) return false
  const day = new Date(date + 'T00:00:00')
  if (Number.isNaN(day.getTime())) return false
  const diffDays = Math.floor((today.getTime() - day.getTime()) / 86_400_000)
  if (window === 'today') return diffDays === 0
  if (window === 'week') return diffDays >= 0 && diffDays < 7
  if (window === 'month') return diffDays >= 0 && diffDays < 30
  return true
}

// filterCatalogItems 筛选：状态按卡片 status；题材按标签/一级分类；
// 上新时段按上线日期窗口（今日/本周/本月）。与 guoapp 一致均为客户端语义。
export function filterCatalogItems(
  items: readonly CatalogItem[],
  state: CatalogFilterState,
  enums: CatalogFilters | null,
): CatalogItem[] {
  const statusLabel = STATUS_LABEL[state.status] ?? ''
  const themeName = enums?.themes.find((t) => t.id === state.theme)?.name ?? state.theme
  return items.filter((item) => {
    if (statusLabel && item.status !== statusLabel) return false
    if (state.theme && !(item.tags?.includes(themeName) || item.category === themeName)) {
      return false
    }
    if (state.onlineTime && !withinOnlineWindow(item.online_date, state.onlineTime, new Date())) {
      return false
    }
    return true
  })
}

export function hasActiveFilters(state: CatalogFilterState): boolean {
  return Boolean(state.theme || state.status || state.onlineTime)
}
