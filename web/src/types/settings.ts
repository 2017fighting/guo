// 设置对象 —— 与后端 internal/server settings.go 的 JSON 字段一一对应。
// api_key 只进不出：GET 恒为空串，是否已配置由 api_key_set 表达；
// PUT 提交空 api_key 表示沿用已存 Key。

export interface JellyfinSettings {
  url: string
  api_key: string
  api_key_set: boolean
}

export interface ProxySettings {
  proxy_url: string
  proxy_enabled: boolean
}

export interface Settings {
  ass_export: boolean
  concurrency: number
  jellyfin: JellyfinSettings
  proxy: ProxySettings
}
