package rankings

// 榜单缓存测试：内存 10 分钟窗口（时钟注入）、源站失败当日磁盘兜底、
// 跨日失效、榜单/游标校验。

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/2017fighting/guo/internal/store"
)

// fakeSource 计数的榜单源桩。
type fakeSource struct {
	calls   int
	cursor  string
	pages   map[string]*BoardPage // board -> 页
	err     error
	failFor int // 前 N 次调用返回 err（模拟源站故障）
}

func (f *fakeSource) Board(ctx context.Context, boardID string, offset int, cursor string) (*BoardPage, error) {
	f.calls++
	f.cursor = cursor
	if f.failFor > 0 {
		f.failFor--
		return nil, f.err
	}
	page, ok := f.pages[boardID]
	if !ok {
		return nil, errors.New("榜单源未配置该榜")
	}
	return page, nil
}

func boardPageForTest() *BoardPage {
	return &BoardPage{
		Items:  []Item{{SeriesID: "7101", Title: "剧一", Rank: 1, Heat: "100万热度"}},
		Offset: 10, SessionID: "sess-1", FilterIDs: []string{"7101"},
		RankVersion: "1791603600", HasMore: true,
	}
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestCacheMemoryWindowSkipsSource(t *testing.T) {
	source := &fakeSource{pages: map[string]*BoardPage{"ranklist_hot_sc": boardPageForTest()}}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, beijingZone())
	cache := NewCache(source, nil)
	cache.Now = func() time.Time { return now }

	first, err := cache.Page(context.Background(), "ranklist_hot_sc", 0, "")
	if err != nil {
		t.Fatalf("首次拉取失败: %v", err)
	}
	if first.UpdatedAt != now.Unix() {
		t.Fatalf("updated_at 应为取数时刻: %d", first.UpdatedAt)
	}
	now = now.Add(9 * time.Minute)
	if _, err := cache.Page(context.Background(), "ranklist_hot_sc", 0, ""); err != nil {
		t.Fatalf("10 分钟内重复请求失败: %v", err)
	}
	if source.calls != 1 {
		t.Fatalf("10 分钟内不应打源站（期望 1 次，实际 %d）", source.calls)
	}
	now = now.Add(2 * time.Minute) // 越过 10 分钟窗口
	if _, err := cache.Page(context.Background(), "ranklist_hot_sc", 0, ""); err != nil {
		t.Fatalf("窗口过期后重取失败: %v", err)
	}
	if source.calls != 2 {
		t.Fatalf("窗口过期后应重新打源站（期望 2 次，实际 %d）", source.calls)
	}
}

func TestCachePersistsToDisk(t *testing.T) {
	source := &fakeSource{pages: map[string]*BoardPage{"ranklist_hot_sc": boardPageForTest()}}
	st := openStore(t)
	cache := NewCache(source, st)
	if _, err := cache.Page(context.Background(), "ranklist_hot_sc", 0, ""); err != nil {
		t.Fatalf("拉取失败: %v", err)
	}
	payload, updatedAt, err := st.RankingsCache("ranklist_hot_sc", 0)
	if err != nil {
		t.Fatalf("磁盘应有一页缓存: %v", err)
	}
	if payload == "" || updatedAt == 0 {
		t.Fatalf("缓存行异常: %q/%d", payload, updatedAt)
	}
	if !strings.Contains(payload, "剧一") || !strings.Contains(payload, `"session_id":"r.`) {
		t.Fatalf("缓存 payload 应含条目与组合游标: %s", payload)
	}
}

func TestCacheSameDayStaleFallback(t *testing.T) {
	st := openStore(t)
	stale := time.Date(2026, 10, 10, 8, 0, 0, 0, beijingZone()) // 当天早些时候
	payload := `{"list":"ranklist_prestige","items":[{"series_id":"7101","title":"旧榜剧","rank":1}],"offset":10,"session_id":"r.cg==","has_more":false,"updated_at":` +
		strconv.FormatInt(stale.Unix(), 10) + `}`
	if err := st.SaveRankingsCache("ranklist_prestige", 0, payload, stale.Unix()); err != nil {
		t.Fatalf("预置缓存失败: %v", err)
	}
	source := &fakeSource{err: errors.New("源站 500"), failFor: 100}
	now := time.Date(2026, 10, 10, 20, 0, 0, 0, beijingZone()) // 同日（东八区）
	cache := NewCache(source, st)
	cache.Now = func() time.Time { return now }

	page, err := cache.Page(context.Background(), "ranklist_prestige", 0, "")
	if err != nil {
		t.Fatalf("源站失败应回退当日磁盘旧榜: %v", err)
	}
	if page.Items[0].Title != "旧榜剧" {
		t.Fatalf("应返回磁盘旧榜内容: %+v", page.Items)
	}
	if page.UpdatedAt != stale.Unix() {
		t.Fatalf("旧榜应带原 updated_at（%d），实际 %d", stale.Unix(), page.UpdatedAt)
	}
}

func TestCacheCrossDayInvalidates(t *testing.T) {
	st := openStore(t)
	yesterday := time.Date(2026, 10, 9, 20, 0, 0, 0, beijingZone())
	payload := `{"list":"ranklist_prestige","items":[],"offset":10,"has_more":false,"updated_at":` + strconv.FormatInt(yesterday.Unix(), 10) + `}`
	if err := st.SaveRankingsCache("ranklist_prestige", 0, payload, yesterday.Unix()); err != nil {
		t.Fatalf("预置缓存失败: %v", err)
	}
	source := &fakeSource{err: errors.New("源站 500"), failFor: 100}
	now := time.Date(2026, 10, 10, 8, 0, 0, 0, beijingZone()) // 次日
	cache := NewCache(source, st)
	cache.Now = func() time.Time { return now }

	if _, err := cache.Page(context.Background(), "ranklist_prestige", 0, ""); err == nil {
		t.Fatal("跨日旧榜不应再返回（无当日兜底即报错）")
	}
}

func TestCacheCrossDayBoundaryEastEight(t *testing.T) {
	st := openStore(t)
	// 东八区 10-09 23:00 写入，东八区 10-10 01:00 请求 → 跨零点失效
	moment := time.Date(2026, 10, 9, 23, 0, 0, 0, beijingZone())
	nextDay := time.Date(2026, 10, 10, 1, 0, 0, 0, beijingZone())
	payload := `{"list":"ranklist_prestige","items":[],"offset":10,"has_more":false,"updated_at":` + strconv.FormatInt(moment.Unix(), 10) + `}`
	if err := st.SaveRankingsCache("ranklist_prestige", 0, payload, moment.Unix()); err != nil {
		t.Fatalf("预置缓存失败: %v", err)
	}
	source := &fakeSource{err: errors.New("源站 500"), failFor: 100}
	cache := NewCache(source, st)
	cache.Now = func() time.Time { return nextDay }
	if _, err := cache.Page(context.Background(), "ranklist_prestige", 0, ""); err == nil {
		t.Fatal("东八区跨零点后旧榜应失效")
	}
}

func TestCacheValidatesList(t *testing.T) {
	source := &fakeSource{pages: map[string]*BoardPage{}}
	cache := NewCache(source, nil)
	if _, err := cache.Page(context.Background(), "bogus_list", 0, ""); err == nil {
		t.Fatal("未知榜单应报错")
	}
	if source.calls != 0 {
		t.Fatal("未知榜单不应打源站")
	}
}

func TestCacheOffsetRequiresCursor(t *testing.T) {
	source := &fakeSource{pages: map[string]*BoardPage{"ranklist_hot_sc": boardPageForTest()}}
	cache := NewCache(source, nil)
	if _, err := cache.Page(context.Background(), "ranklist_hot_sc", 10, ""); err == nil {
		t.Fatal("offset>0 缺游标应报错")
	}
	if source.calls != 0 {
		t.Fatal("缺游标不应打源站")
	}
}

func TestCacheCursorPassedThrough(t *testing.T) {
	source := &fakeSource{pages: map[string]*BoardPage{"ranklist_hot_sc": boardPageForTest()}}
	cache := NewCache(source, nil)
	page, err := cache.Page(context.Background(), "ranklist_hot_sc", 0, "")
	if err != nil {
		t.Fatalf("首页失败: %v", err)
	}
	if page.SessionID == "" || page.SessionID == "sess-1" {
		t.Fatalf("对外 session_id 应为组合游标（r. 前缀），实际 %q", page.SessionID)
	}
	if _, err := cache.Page(context.Background(), "ranklist_hot_sc", page.Offset, page.SessionID); err != nil {
		t.Fatalf("带游标翻页失败: %v", err)
	}
	if source.cursor != page.SessionID {
		t.Fatalf("源站应收到透传的组合游标: %q vs %q", source.cursor, page.SessionID)
	}
}
