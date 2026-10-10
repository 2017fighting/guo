package hongguo

// 真机冒烟：GUO_LIVE=1 时才执行（不打进常规测试/CI）。
// 记录双通道搜索与联想的实际响应形态，验证协议假设。

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestSearchLiveSmoke(t *testing.T) {
	if os.Getenv("GUO_LIVE") != "1" {
		t.Skip("GUO_LIVE=1 才跑真机冒烟")
	}
	c := NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	res, err := c.Search(ctx, "白月光")
	if err != nil {
		t.Fatalf("真机双通道搜索失败: %v", err)
	}
	t.Logf("搜索: 条目=%d limited=%v 警告=%v query=%q", len(res.Items), res.Limited, res.Warnings, res.Query)
	if len(res.Items) == 0 {
		t.Fatal("真机搜索未返回条目")
	}
	first, _ := json.Marshal(res.Items[0])
	t.Logf("首条目 JSON: %s", first)

	items, err := c.Suggest(ctx, "白月光")
	if err != nil {
		t.Fatalf("真机联想失败: %v", err)
	}
	t.Logf("联想: 条目=%d", len(items))
	for i, item := range items {
		if i >= 3 {
			break
		}
		t.Logf("联想[%d]: name=%q type=%q series_id=%q", i, item.Name, item.Type, item.SeriesID)
	}
}
