// 字节/速度/剩余时间的人话展示（队列页进度行）。

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 MB'
  if (bytes >= 1 << 30) return `${round(bytes / (1 << 30))} GB`
  if (bytes >= 1 << 20) return `${round(bytes / (1 << 20))} MB`
  if (bytes >= 1 << 10) return `${round(bytes / (1 << 10))} KB`
  return `${Math.round(bytes)} B`
}

export function formatSpeed(bps: number): string {
  if (!Number.isFinite(bps) || bps <= 0) return ''
  return `${formatBytes(bps)}/s`
}

export function formatEta(remainingBytes: number, speedBps: number): string {
  if (speedBps <= 0 || remainingBytes <= 0) return ''
  const seconds = Math.round(remainingBytes / speedBps)
  if (seconds < 60) return `预计剩余 ${seconds} 秒`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `预计剩余 ${minutes} 分 ${seconds % 60} 秒`
  const hours = Math.floor(minutes / 60)
  return `预计剩余 ${hours} 小时 ${minutes % 60} 分`
}

function round(value: number): string {
  return value >= 100 ? value.toFixed(0) : value.toFixed(1).replace(/\.0$/, '')
}
