// 最近搜索：localStorage 持久化（键 guo-recent-searches，≤20 条，新搜索置顶去重）。
// 腐败数据（非字符串数组）按空处理，不抛错。

const KEY = 'guo-recent-searches'
const LIMIT = 20

export function loadRecentSearches(): string[] {
  try {
    const raw = localStorage.getItem(KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.filter((v): v is string => typeof v === 'string' && v.trim() !== '').slice(0, LIMIT)
  } catch {
    return []
  }
}

function save(list: readonly string[]): string[] {
  const capped = list.slice(0, LIMIT)
  try {
    localStorage.setItem(KEY, JSON.stringify(capped))
  } catch {
    // 存储不可用（隐私模式等）只影响持久化，不影响当次会话
  }
  return capped
}

/** 记录一次搜索：去重置顶，超限截断；空白不记录。返回新列表。 */
export function addRecentSearch(query: string): string[] {
  const q = query.trim()
  const current = loadRecentSearches()
  if (!q) return current
  return save([q, ...current.filter((v) => v !== q)])
}

/** 单删一条最近搜索。 */
export function removeRecentSearch(query: string): string[] {
  return save(loadRecentSearches().filter((v) => v !== query))
}

/** 清空最近搜索。 */
export function clearRecentSearches(): string[] {
  return save([])
}
