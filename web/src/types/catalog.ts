// 目录条目/筛选项类型 —— 与后端 internal/hongguo CatalogItem/CatalogPage/CatalogFilters
// 的 JSON 字段一一对应（蛇形命名，保持后端口径便于对账）。

export type GenreKey = 'all' | 'short_play' | 'comic_series' | 'ai_series'

export interface CatalogItem {
  series_id: string
  title: string
  cover: string
  episode_count: string
  status: '' | '完结' | '连载'
  heat: string
  play_count: string
  online_date: string
  vertical: boolean
  vip?: boolean
  category?: string
  tags?: string[]
}

export interface CatalogPage {
  genre: string
  items: CatalogItem[]
  offset: number
  session_id: string
  has_more: boolean
}

export interface CatalogFilterOption {
  id: string
  name: string
}

export interface CatalogFilters {
  themes: CatalogFilterOption[]
  statuses: CatalogFilterOption[]
  online_times: CatalogFilterOption[]
  source: string
}
