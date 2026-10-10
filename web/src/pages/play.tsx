// 播放页占位：详情页「立即播放」/选集的落点路由（/play/:seriesID/:vid）。
// 真实播放页（流代理 + 弹幕 Canvas）由 play lane（#13）替换本文件。

import { Link, useParams } from 'react-router-dom'
import { Clapperboard } from 'lucide-react'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'

export function PlayPage() {
  const { seriesID = '', vid = '' } = useParams()
  return (
    <Card className="mx-auto max-w-md">
      <CardContent className="flex flex-col items-center gap-3 px-6 py-12 text-center">
        <Clapperboard className="size-10 text-muted-foreground" aria-hidden />
        <h1 className="text-lg font-semibold">播放页建设中</h1>
        <p className="text-sm text-muted-foreground">
          在线播放（弹幕叠加、画质/线路切换）即将上线。当前可以先下载本剧到 Jellyfin 离线看。
        </p>
        <Button asChild variant="outline">
          <Link to={`/drama/${seriesID}`}>返回详情</Link>
        </Button>
        <p className="text-xs text-muted-foreground">series: {seriesID} · vid: {vid}</p>
      </CardContent>
    </Card>
  )
}
