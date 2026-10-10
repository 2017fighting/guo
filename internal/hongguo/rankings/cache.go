package rankings

// 榜单缓存门面（spec §5/§10）：内存 10 分钟 + SQLite 磁盘当日 stale 兜底。
// 不做旧官网 4 榜降级；源站失败且无当日磁盘数据时直接报错。

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"
)

// cacheTTL 内存缓存窗口。
const cacheTTL = 10 * time.Minute

// beijingZone 数据更新时间的「当日」判定口径（东八区，与 hongguo 包一致）。
func beijingZone() *time.Location {
	return time.FixedZone("CST", 8*3600)
}

// CacheStore 榜单页磁盘持久化接口（*store.Store 实现）。
type CacheStore interface {
	SaveRankingsCache(list string, offset int, payload string, updatedAt int64) error
	RankingsCache(list string, offset int) (string, int64, error)
}

// Source 榜单源接口（*Client 实现；缓存测试与 server handler 复用）。
type Source interface {
	Board(ctx context.Context, boardID string, offset int, cursor string) (*BoardPage, error)
}

// Page /api/v1/rankings 响应体（也是磁盘缓存 payload 的序列化形态）。
// SessionID 为组合游标（EncodeCursor 产物），客户端原样回传即可翻页。
type Page struct {
	List      string `json:"list"`
	Items     []Item `json:"items"`
	Offset    int    `json:"offset"`
	SessionID string `json:"session_id"`
	HasMore   bool   `json:"has_more"`
	UpdatedAt int64  `json:"updated_at"`
}

// Cache 榜单缓存：Source 打 api3，Store 落磁盘；Now 可注入（测试）。
type Cache struct {
	Source Source
	Store  CacheStore // nil = 仅内存
	TTL    time.Duration
	Now    func() time.Time

	mu     sync.Mutex
	memory map[string]Page
}

// NewCache 创建缓存门面（store 可为 nil）。
func NewCache(source Source, store CacheStore) *Cache {
	return &Cache{Source: source, Store: store}
}

func (c *Cache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Cache) ttl() time.Duration {
	if c.TTL > 0 {
		return c.TTL
	}
	return cacheTTL
}

func cacheKey(list string, offset int) string {
	return list + "\x00" + strconv.Itoa(offset)
}

// Page 取一页榜单（list ∈ 8 榜；offset=0 首页，翻页带上一页游标）。
// 内存命中（TTL 内）直接返回；否则打源站并写盘；源站失败回退当日
// 磁盘旧榜（带原 updated_at），跨日或无缓存即报错。
func (c *Cache) Page(ctx context.Context, list string, offset int, cursor string) (*Page, error) {
	if _, ok := BoardByID(list); !ok {
		return nil, errors.New("未知榜单，请从 8 榜中选择")
	}
	if offset < 0 {
		return nil, errors.New("榜单 offset 不能为负")
	}
	if offset > 0 && cursor == "" {
		return nil, errors.New("翻页缺少上一页游标，请从第一页重新加载")
	}
	key := cacheKey(list, offset)
	c.mu.Lock()
	cached, hit := c.memory[key]
	c.mu.Unlock()
	if hit && c.now().Unix()-cached.UpdatedAt < int64(c.ttl().Seconds()) {
		page := cached
		return &page, nil
	}
	board, err := c.Source.Board(ctx, list, offset, cursor)
	if err != nil {
		if stale := c.diskFallback(list, offset, c.now()); stale != nil {
			return stale, nil
		}
		return nil, err
	}
	page := Page{
		List: list, Items: board.Items, Offset: board.Offset, HasMore: board.HasMore,
		SessionID: EncodeCursor(Cursor{SessionID: board.SessionID, FilterIDs: board.FilterIDs, RankVersion: board.RankVersion}),
		UpdatedAt: c.now().Unix(),
	}
	if c.Store != nil {
		if payload, err := json.Marshal(page); err == nil {
			_ = c.Store.SaveRankingsCache(list, offset, string(payload), page.UpdatedAt) // 磁盘兜底尽力写
		}
	}
	if c.memory == nil {
		c.memory = map[string]Page{}
	}
	c.mu.Lock()
	c.memory[key] = page
	c.mu.Unlock()
	return &page, nil
}

// diskFallback 源站失败时的当日磁盘旧榜；跨日/缺行/解析失败返回 nil。
func (c *Cache) diskFallback(list string, offset int, now time.Time) *Page {
	if c.Store == nil {
		return nil
	}
	payload, updatedAt, err := c.Store.RankingsCache(list, offset)
	if err != nil || payload == "" {
		return nil
	}
	if !sameBeijingDay(time.Unix(updatedAt, 0), now) {
		return nil
	}
	var page Page
	if json.Unmarshal([]byte(payload), &page) != nil || page.List == "" || len(page.Items) == 0 {
		return nil
	}
	page.UpdatedAt = updatedAt
	return &page
}

// sameBeijingDay 两个时刻是否同处东八区同一自然日。
func sameBeijingDay(a, b time.Time) bool {
	zone := beijingZone()
	return a.In(zone).Format("2006-01-02") == b.In(zone).Format("2006-01-02")
}
