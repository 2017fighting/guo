package hongguo

// 真机冒烟：GUO_LIVE=1 时才执行（不打进常规测试/CI）。
// 记录 landpage 实际响应形态与筛选面板探测结果，验证协议假设。

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestCatalogLiveSmoke(t *testing.T) {
	if os.Getenv("GUO_LIVE") != "1" {
		t.Skip("GUO_LIVE=1 才跑真机冒烟")
	}
	c := NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	page, err := c.CatalogPage(ctx, CatalogQuery{Genre: CatalogGenreShortPlay})
	if err != nil {
		t.Fatalf("真机 landpage 失败: %v", err)
	}
	if len(page.Items) == 0 {
		t.Fatal("真机 landpage 未返回条目")
	}
	first, _ := json.Marshal(page.Items[0])
	t.Logf("首条目 JSON: %s", first)
	t.Logf("游标: offset=%d session=%q has_more=%v 条目数=%d", page.Offset, page.SessionID, page.HasMore, len(page.Items))
	if page.HasMore {
		page2, err := c.CatalogPage(ctx, CatalogQuery{Genre: CatalogGenreShortPlay, Offset: page.Offset, SessionID: page.SessionID})
		if err != nil {
			t.Fatalf("真机翻页失败: %v", err)
		}
		t.Logf("第二页: 条目=%d offset=%d has_more=%v", len(page2.Items), page2.Offset, page2.HasMore)
	}

	// 筛选面板探测：看真机是否给面板 schema
	payload := catalogLandpagePayload(CatalogGenreShortPlay, 0, "")
	payload["need_selector_panel"] = true
	result, err := c.appRequest(ctx, "POST", "/reading/distribution/category/landpage/v/", nil, payload, false)
	if err != nil {
		t.Logf("面板探测请求失败: %v", err)
	} else {
		data, _ := result["data"].(map[string]any)
		keys := make([]string, 0, len(data))
		for key := range data {
			keys = append(keys, key)
		}
		t.Logf("面板探测 data 键: %v", keys)
		if panel, ok := data["selector_panel"]; ok {
			raw, _ := json.Marshal(panel)
			t.Logf("selector_panel: %s", raw)
		}
	}
}
