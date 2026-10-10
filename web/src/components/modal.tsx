// 轻量弹层：mockups 的模态语义（聚焦决策 + 唯一主行动），
// 无新依赖实现（fixed 遮罩 + card 内容），ESC/点遮罩关闭。

import { X } from 'lucide-react'
import { useEffect, type ReactNode } from 'react'
import { Card, CardContent } from '@/components/ui/card'

export function Modal({
  title,
  onClose,
  children,
}: {
  title: string
  onClose: () => void
  children: ReactNode
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      role="presentation"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose()
      }}
    >
      <Card
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className="w-full max-w-[560px] border-border"
        onClick={(e) => e.stopPropagation()}
      >
        <CardContent className="flex flex-col gap-4 px-4 py-4">
          <div className="flex items-center justify-between">
            <h2 className="text-lg font-semibold">{title}</h2>
            <button
              type="button"
              aria-label="关闭"
              className="rounded-md p-1 text-muted-foreground hover:text-foreground"
              onClick={onClose}
            >
              <X className="size-4" aria-hidden />
            </button>
          </div>
          {children}
        </CardContent>
      </Card>
    </div>
  )
}
