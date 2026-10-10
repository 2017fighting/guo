// 选集 Miller 分组（50 集/组）：详情页选集、下载弹层、播放页三处共用，
// 保证组边界与「当前组」判定口径一致。

export const GROUP_SIZE = 50

export interface EpisodeGroup {
  start: number
  end: number
}

// episodeGroups 按集号 1..total 切分连续分组（total=0 返回空）。
export function episodeGroups(total: number): EpisodeGroup[] {
  const out: EpisodeGroup[] = []
  for (let i = 0; i < total; i += GROUP_SIZE) {
    out.push({ start: i + 1, end: Math.min(i + GROUP_SIZE, total) })
  }
  return out
}

// episodeGroupAt 找集号 index 所在组；无组（空列表）返回 undefined。
export function episodeGroupAt(groups: readonly EpisodeGroup[], index: number): EpisodeGroup | undefined {
  return groups.find((g) => index >= g.start && index <= g.end)
}

// episodesInGroup 取某组内的分集（按 ep.index 落在组区间过滤）。
export function episodesInGroup<T extends { index: number }>(
  episodes: readonly T[],
  group: EpisodeGroup | undefined,
): T[] {
  if (!group) return []
  return episodes.filter((e) => e.index >= group.start && e.index <= group.end)
}
