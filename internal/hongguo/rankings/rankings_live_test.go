package rankings

// 真机冒烟：GUO_LIVE=1 时才执行（house pattern，见 internal/hongguo/catalog_live_test.go）。
// 覆盖 8 榜首页 + 一条翻页链路；验证轻量 Gorgon（不带 Argus/Ladon/Helios/Medusa）
// 是否仍被 api3 接受——若失败会打印风控拦截形态（HTTP 200 空体），见包注释。

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestRankingsLiveSmoke(t *testing.T) {
	if os.Getenv("GUO_LIVE") != "1" {
		t.Skip("GUO_LIVE=1 才跑真机冒烟")
	}
	c := NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	boards, err := c.Boards(ctx)
	if err != nil {
		t.Fatalf("plan 分类失败: %v", err)
	}
	t.Logf("plan taxonomy: %d 板", len(boards))

	for _, board := range Boards {
		board := board
		t.Run(board.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			first, err := c.Board(ctx, board.ID, 0, "")
			if err != nil {
				t.Fatalf("真机首页失败: %v", err)
			}
			if len(first.Items) == 0 {
				t.Fatalf("真机 %s 未返回条目", board.ID)
			}
			if first.Items[0].Rank != 1 {
				t.Errorf("首条 rank 应为 1，实际 %d（%s）", first.Items[0].Rank, first.Items[0].Title)
			}
			t.Logf("%s[%s]: %d 条，#1 %s（%s）has_more=%v next=%d",
				board.Name, board.ID, len(first.Items), first.Items[0].Title, first.Items[0].Heat, first.HasMore, first.Offset)
			if !first.HasMore {
				return
			}
			cursor := EncodeCursor(Cursor{SessionID: first.SessionID, FilterIDs: first.FilterIDs, RankVersion: first.RankVersion})
			second, err := c.Board(ctx, board.ID, first.Offset, cursor)
			if err != nil {
				t.Fatalf("真机翻页失败: %v", err)
			}
			if len(second.Items) == 0 || second.Offset <= first.Offset {
				t.Fatalf("翻页未前进: %d 条 next=%d", len(second.Items), second.Offset)
			}
			t.Logf("第二页: %d 条，next=%d has_more=%v", len(second.Items), second.Offset, second.HasMore)
		})
	}
}
