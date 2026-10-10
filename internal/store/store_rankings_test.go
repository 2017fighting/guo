package store

// rankings_cache 表（spec §5）：榜单页磁盘 stale 兜底，(list, offset) 主键。

import (
	"testing"
	"time"
)

func TestRankingsCacheRoundTrip(t *testing.T) {
	st := open(t)
	payload := `{"list":"ranklist_hot_sc","items":[],"offset":10,"updated_at":100}`
	if err := st.SaveRankingsCache("ranklist_hot_sc", 10, payload, 100); err != nil {
		t.Fatalf("写入榜单缓存失败: %v", err)
	}
	got, updatedAt, err := st.RankingsCache("ranklist_hot_sc", 10)
	if err != nil {
		t.Fatalf("读取榜单缓存失败: %v", err)
	}
	if got != payload || updatedAt != 100 {
		t.Fatalf("读回不符: payload=%q updated_at=%d", got, updatedAt)
	}
}

func TestRankingsCacheUpsert(t *testing.T) {
	st := open(t)
	if err := st.SaveRankingsCache("ranklist_prestige", 0, "v1", 1); err != nil {
		t.Fatalf("首次写入失败: %v", err)
	}
	if err := st.SaveRankingsCache("ranklist_prestige", 0, "v2", 2); err != nil {
		t.Fatalf("覆盖写入失败: %v", err)
	}
	got, updatedAt, err := st.RankingsCache("ranklist_prestige", 0)
	if err != nil || got != "v2" || updatedAt != 2 {
		t.Fatalf("覆盖后读回应为 v2/2，实际 %q/%d err=%v", got, updatedAt, err)
	}
}

func TestRankingsCacheNotFound(t *testing.T) {
	st := open(t)
	if _, _, err := st.RankingsCache("no_such_list", 0); err != ErrNotFound {
		t.Fatalf("缺行应返回 ErrNotFound，实际 %v", err)
	}
}

func TestRankingsCacheDifferentOffsets(t *testing.T) {
	st := open(t)
	for _, offset := range []int{0, 10, 20} {
		if err := st.SaveRankingsCache("human_hot_play", offset, "p", time.Now().Unix()); err != nil {
			t.Fatalf("offset=%d 写入失败: %v", offset, err)
		}
	}
	for _, offset := range []int{0, 10, 20} {
		if _, _, err := st.RankingsCache("human_hot_play", offset); err != nil {
			t.Fatalf("offset=%d 应有缓存行: %v", offset, err)
		}
	}
	if _, _, err := st.RankingsCache("human_hot_play", 30); err != ErrNotFound {
		t.Fatalf("未写入的 offset 应 ErrNotFound，实际 %v", err)
	}
}

