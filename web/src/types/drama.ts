// 详情页类型 —— 与后端 internal/server/drama.go dramaDetailPayload 一一对应。

export interface DramaEpisode {
  index: number
  vid: string
}

export interface DramaDetail {
  series_id: string
  title: string
  year: string
  plot: string
  cover: string
  fanart: string
  genres: string[]
  episodes: DramaEpisode[]
}
