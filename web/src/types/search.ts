// 搜索/联想类型 —— 与后端 internal/hongguo SearchResult / SuggestItem
// 的 JSON 字段一一对应（蛇形命名，保持后端口径便于对账）。

import type { CatalogItem } from '@/types/catalog'

export interface SearchResponse {
  query: string
  items: CatalogItem[]
  limited: boolean
  warnings: string[]
}

export interface SuggestItem {
  name: string
  /** 白名单 word_type（short_play_name/short_play_category/common_query/actor_name/short_play_actor），白名单外为空串 */
  type: string
  /** 剧名联想直达详情；其余联想无此字段 */
  series_id?: string
}
