// 弹幕 Canvas 叠加层：右→左滚动弹幕，样式对齐 ASS 导出口径
// （internal/ass + docs/research/danmaku-ass.md）：
//   - 字号 = 舞台高 × 30/1080（ASS PlayRes 1080 / FontSize 30）
//   - 描边 = max(字号/25, 1)（BorderStyle 1，黑描边白字，无阴影，全不透明）
//   - 字体栈以 ASS FontFace「黑体」开头
//   - 单条生命周期 8s，走完 (W+w) 距离；行占用为追尾模型（对齐 ass.go rowFree）

import { memo, useEffect, useRef } from 'react'
import { DANMAKU_LIFETIME_MS, type DanmakuItem } from '@/types/play'

const FONT_STACK = '"黑体", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "Noto Sans CJK SC", sans-serif'
const PLAY_RES_HEIGHT = 1080
const PLAY_RES_FONT = 30

interface Sprite {
  text: string
  born: number // ms（= offset_ms）
  width: number
  speed: number // px/ms
  row: number
}

interface RowSlot {
  born: number
  width: number
}

function DanmakuOverlayImpl({
  items,
  timeMS,
  enabled,
}: {
  items: DanmakuItem[]
  timeMS: number
  enabled: boolean
}) {
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const itemsRef = useRef(items)
  const timeRef = useRef(timeMS)
  const prevTimeRef = useRef(timeMS)

  // 每帧读取的输入走 ref（rAF 循环不随 render 重建）
  useEffect(() => {
    itemsRef.current = items
    timeRef.current = timeMS
  }, [items, timeMS])

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return
    const ctx = canvas.getContext('2d')
    if (!ctx) return

    const seen = new Set<string>()
    let sprites: Sprite[] = []
    let rows: RowSlot[] = []
    let spawnCursor = 0

    const reset = () => {
      seen.clear()
      sprites = []
      rows = []
      spawnCursor = 0
    }

    const resize = () => {
      const rect = canvas.getBoundingClientRect()
      const dpr = window.devicePixelRatio || 1
      canvas.width = Math.max(1, Math.round(rect.width * dpr))
      canvas.height = Math.max(1, Math.round(rect.height * dpr))
    }
    resize()
    const observer = new ResizeObserver(resize)
    observer.observe(canvas)

    let raf = 0
    const draw = () => {
      raf = requestAnimationFrame(draw)
      const t = timeRef.current
      // 回退跳播：清屏重来（允许已见弹幕重放）
      if (t + DANMAKU_LIFETIME_MS < prevTimeRef.current) reset()
      prevTimeRef.current = t

      const rect = canvas.getBoundingClientRect()
      const dpr = window.devicePixelRatio || 1
      const W = rect.width
      const H = rect.height
      if (W < 2 || H < 2) return
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
      ctx.clearRect(0, 0, W, H)
      if (!enabled) return

      const fontSize = (H / PLAY_RES_HEIGHT) * PLAY_RES_FONT
      const outline = Math.max((PLAY_RES_FONT / 25) * (H / PLAY_RES_HEIGHT), 1)
      ctx.font = `${fontSize}px ${FONT_STACK}`
      ctx.textBaseline = 'top'
      ctx.lineJoin = 'round'
      const rowH = fontSize
      const rowCount = Math.max(1, Math.floor(H / rowH))

      // 入屏：扫描到当前时间为止的新弹幕
      const list = itemsRef.current
      while (spawnCursor < list.length && list[spawnCursor].offset_ms <= t) {
        const item = list[spawnCursor++]
        const key = item.id ?? `${item.offset_ms}:${item.text}`
        if (seen.has(key)) continue
        seen.add(key)
        const width = ctx.measureText(item.text).width
        const speed = (W + width) / DANMAKU_LIFETIME_MS
        // 追尾模型（对齐 ass.go rowFree）：找第一条空闲行，满了叠到最早行
        let row = -1
        for (let i = 0; i < rowCount; i++) {
          const slot = rows[i]
          if (!slot || rowFree(slot, t, width, W)) {
            row = i
            break
          }
        }
        if (row < 0) {
          let earliest = 0
          for (let i = 1; i < rowCount; i++) {
            if ((rows[i]?.born ?? Infinity) < (rows[earliest]?.born ?? Infinity)) earliest = i
          }
          row = earliest
        }
        rows[row] = { born: t, width }
        sprites.push({ text: item.text, born: item.offset_ms, width, speed, row })
      }

      // 滚动绘制
      const alive: Sprite[] = []
      for (const sprite of sprites) {
        const x = W - (t - sprite.born) * sprite.speed
        if (x + sprite.width < 0) continue
        alive.push(sprite)
        ctx.lineWidth = outline * 2
        ctx.strokeStyle = '#000'
        ctx.strokeText(sprite.text, x, sprite.row * rowH)
        ctx.fillStyle = '#fff'
        ctx.fillText(sprite.text, x, sprite.row * rowH)
      }
      sprites = alive
    }
    raf = requestAnimationFrame(draw)

    return () => {
      cancelAnimationFrame(raf)
      observer.disconnect()
      ctx.clearRect(0, 0, canvas.width, canvas.height)
    }
  }, [enabled])

  return (
    <canvas
      ref={canvasRef}
      aria-hidden
      className="pointer-events-none absolute inset-0 h-full w-full"
    />
  )
}

// 追尾占用判定（ass.go rowFree 同口径）：
//   旧弹幕入屏太晚，或旧弹幕尾部尚未完全入屏 → 该行占用。
function rowFree(old: RowSlot, now: number, newWidth: number, W: number): boolean {
  const dm = DANMAKU_LIFETIME_MS
  const wn = newWidth
  const wo = old.width
  if (old.born > now - dm * (1 - W / (wn + W))) return false
  if (old.born + (dm * wo) / (wo + W) > now) return false
  return true
}

export const DanmakuOverlay = memo(DanmakuOverlayImpl)
