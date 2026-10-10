export { cn } from "cn"

// dedupBy 按键去重、保留首见顺序：浏览/榜单无限加载时跨页重复条目的兑底。
export function dedupBy<T>(items: readonly T[], key: (item: T) => string): T[] {
  const seen = new Set<string>()
  const out: T[] = []
  for (const item of items) {
    const k = key(item)
    if (seen.has(k)) continue
    seen.add(k)
    out.push(item)
  }
  return out
}
