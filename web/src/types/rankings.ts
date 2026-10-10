// 排行榜类型 —— 与后端 internal/hongguo/rankings 的 Board/Item/Page
// JSON 字段一一对应（蛇形命名，保持后端口径便于对账）。

export interface RankingItem {
  series_id: string
  title: string
  cover: string
  desc: string
  vertical: boolean
  rank: number
  heat: string
  score: string
  play_count: string
  episode_count: string
}

export interface RankingsPage {
  list: string
  items: RankingItem[]
  offset: number
  session_id: string // 组合游标（r. 前缀），翻页原样回传
  has_more: boolean
  updated_at: number // unix 秒，数据更新时间
}

// 8 榜 Tab（id 即 /api/v1/rankings?list= 取值，与后端 rankings.Boards 同序）。
export const RANKING_BOARDS: ReadonlyArray<{ id: string; label: string }> = [
  { id: 'ranklist_hot_sc', label: '热门' },
  { id: 'ranklist_hot_play_sc', label: '热播' },
  { id: 'ranklist_prestige', label: '口碑' },
  { id: 'ranklist_new_rank_sc', label: '上新' },
  { id: 'ranklist_must_watch', label: '必看' },
  { id: 'human_hot_play', label: '真人热播' },
  { id: 'comic_series_hot_rank', label: '漫剧热榜' },
  { id: 'ai_playlet_hot_sc', label: 'AI热门' },
]
