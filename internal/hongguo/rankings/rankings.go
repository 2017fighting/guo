// Package rankings 实现红果 api3 榜单通道客户端（spec §6/§10，guoapp-reference §5）：
//   - 端点：GET /reading/bookapi/plan/v:version/（入口，发现 cell_id + 榜单分类）
//     与 GET /reading/bookapi/bookmall/cell/change/v:version/（换榜/翻页）
//   - 设备参数/UA 取自抓包真机（version_code=73970、sdk_gphone64_arm64、gray_test_64）
//   - 签名：轻量 Gorgon（X-Gorgon/X-Khronos/X-SS-Req-Ticket）。真机抓包另带短
//     X-Argus/X-Ladon 与 X-Helios/X-Medusa；实测（2026-10）：Argus=LE 时间戳、
//     Ladon/Helios/Medusa 为私有算法不可复算，全部省略即可通过；带错误的短值反而
//     被静默拒绝（HTTP 200 空体）。见 rankings_live_test.go 冒烟。
//   - 游标：next_offset + session_id + filter_ids（上一页 series_id）+ rank_version，
//     组合成对外的 session_id 游标（"r." 前缀 base64），10 条/页。
package rankings

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// api3BaseURL 榜单通道主机（与目录 feed 的 api5 主机不同族）。
const api3BaseURL = "https://api3-normal-sinfonlinec.fqnovel.com"

// rankingsUserAgent 抓包真机 UA（7.3.9.70 / emulator 设备形态）。
const rankingsUserAgent = "com.phoenix.read/73970 (Linux; U; Android 13; en_US; sdk_gphone64_arm64; Build/TE1A.240213.009; Cronet/TTNetVersion:8d40f833 2026-03-03 QuicVersion:21ac1950 2025-11-18)"

const rankingsPageSize = 10

// planTTL plan 结果（cell_id + 榜单分类）进程内缓存时长。
const planTTL = 10 * time.Minute

// Board 我们的 8 榜（spec §10 / #8 决议）。ID 即 /api/v1/rankings?list= 取值，
// 直接采用上游榜单 selector_item_id；Tab 为换榜请求的外层 tab（selected_items）。
//
// 8 榜 → 上游 38 板 taxonomy 映射（plan cell_selector，guoapp-reference §5.5）：
//
//	全站 5 榜取「全部(all)」族：热门=ranklist_hot_sc(推荐榜)、热播=ranklist_hot_play_sc、
//	口碑=ranklist_prestige(臻果榜)、上新=ranklist_new_rank_sc(新剧榜)、必看=ranklist_must_watch；
//	真人热播=human_hot_play（真人剧族 tab=human）；
//	漫剧热榜=comic_series_hot_rank（漫剧族 tab=comic_series_rank）；
//	AI 热门=ai_playlet_hot_sc（AI 剧族 tab=ai_playlet）。
type Board struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Tab  string `json:"tab"`
}

// Boards 8 榜清单（顺序即前端 Tab 顺序）。
var Boards = []Board{
	{ID: "ranklist_hot_sc", Name: "热门", Tab: "all"},
	{ID: "ranklist_hot_play_sc", Name: "热播", Tab: "all"},
	{ID: "ranklist_prestige", Name: "口碑", Tab: "all"},
	{ID: "ranklist_new_rank_sc", Name: "上新", Tab: "all"},
	{ID: "ranklist_must_watch", Name: "必看", Tab: "all"},
	{ID: "human_hot_play", Name: "真人热播", Tab: "human"},
	{ID: "comic_series_hot_rank", Name: "漫剧热榜", Tab: "comic_series_rank"},
	{ID: "ai_playlet_hot_sc", Name: "AI热门", Tab: "ai_playlet"},
}

// BoardByID 查 8 榜定义；ok=false 表示不是我们支持的榜单。
func BoardByID(id string) (Board, bool) {
	for _, b := range Boards {
		if b.ID == id {
			return b, true
		}
	}
	return Board{}, false
}

// BoardNames 8 榜 id → 名称映射（前端 Tab 文案）。
func BoardNames() map[string]string {
	names := make(map[string]string, len(Boards))
	for _, b := range Boards {
		names[b.ID] = b.Name
	}
	return names
}

// Item 榜单条目（上游 cell_view.cell_data[].video_data[] 规范化）：
// title/cover/video_desc/series_id/vertical 为 spec §6 指定字段；
// Rank 来自 recommend_info（JSON 字符串）的 rank，样本存在重复值需容错；
// Heat 来自 secondary_info_list 的热度文本（口碑榜为评分/收藏文本）。
type Item struct {
	SeriesID     string `json:"series_id"`
	Title        string `json:"title"`
	Cover        string `json:"cover"`
	Desc         string `json:"desc"`
	Vertical     bool   `json:"vertical"`
	Rank         int    `json:"rank"`
	Heat         string `json:"heat"`
	Score        string `json:"score"`
	PlayCount    string `json:"play_count"`
	EpisodeCount string `json:"episode_count"`
}

// BoardPage 上游一页 + 翻页游标三件套（FilterIDs = 本页 series_id，
// 下一页请求作为 filter_ids 回传）。
type BoardPage struct {
	Items       []Item
	Offset      int
	SessionID   string
	FilterIDs   []string
	RankVersion string
	HasMore     bool
}

// BoardInfo plan 分类里的单个榜单（比静态 8 榜多覆盖未启用的 38 板）。
type BoardInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Tab  string `json:"tab"`
}

// ---- 组合游标（对 /api/v1/rankings 暴露的 session_id 载荷） ----

const cursorPrefix = "r."

// Cursor 翻页游标：上游 session_id + filter_ids（上一页 series_id）+ rank_version。
type Cursor struct {
	SessionID   string   `json:"s,omitempty"`
	FilterIDs   []string `json:"f,omitempty"`
	RankVersion string   `json:"v,omitempty"`
}

// EncodeCursor 编码组合游标（上游 session 为日志 id 形态，加前缀区分）。
func EncodeCursor(c Cursor) string {
	content, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	return cursorPrefix + base64.RawURLEncoding.EncodeToString(content)
}

// DecodeCursor 解码组合游标；非本族游标返回 ok=false（调用方按缺游标处理）。
func DecodeCursor(s string) (Cursor, bool) {
	if !strings.HasPrefix(s, cursorPrefix) {
		return Cursor{}, false
	}
	content, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(s, cursorPrefix))
	if err != nil || len(content) > 8192 {
		return Cursor{}, false
	}
	var c Cursor
	if json.Unmarshal(content, &c) != nil {
		return Cursor{}, false
	}
	return c, true
}

// Client api3 榜单客户端。设备/会话身份（device_id、iid、session_uuid 等）
// 进程内固定，测试通过 BaseURL/HTTP 注入假服务。
type Client struct {
	HTTP      *http.Client
	BaseURL   string // 测试注入用，默认 api3BaseURL
	UserAgent string // 默认 rankingsUserAgent

	identity api3Identity

	mu      sync.Mutex
	plan    *planResult
	planAt  time.Time
	planErr error
	nowTTL  time.Duration // 测试可缩短 plan 缓存
}

// NewClient 创建榜单客户端（新设备身份）。
func NewClient() *Client {
	return &Client{identity: newAPI3Identity()}
}

// planResult plan 响应缓存：cell_id（cell/change 必带）+ 榜单分类。
type planResult struct {
	CellID string
	Boards []BoardInfo
}

// Boards 拉取榜单分类（plan cell_selector；38 板 taxonomy）。plan 不可用时回退
// 静态 8 榜映射（换榜不阻塞），只对未知形态报错由调用方降级。
func (c *Client) Boards(ctx context.Context) ([]BoardInfo, error) {
	result, err := c.ensurePlan(ctx)
	if err != nil || result == nil {
		boards := make([]BoardInfo, 0, len(Boards))
		for _, b := range Boards {
			boards = append(boards, BoardInfo{ID: b.ID, Name: b.Name, Tab: b.Tab})
		}
		return boards, nil
	}
	return result.Boards, nil
}

// Board 拉取一页榜单。offset=0 且无游标 = 换榜（unlimited_selector_change_type=2）；
// 否则翻页（type=1，回传 offset/session_id/filter_ids/rank_version）。
// cursor 为上一页 EncodeCursor 的产物。
func (c *Client) Board(ctx context.Context, boardID string, offset int, cursor string) (*BoardPage, error) {
	board, ok := BoardByID(boardID)
	if !ok {
		return nil, fmt.Errorf("未知榜单 %s（支持：%s 等 8 榜）", boardID, Boards[0].ID)
	}
	if offset < 0 || offset%rankingsPageSize != 0 {
		return nil, errors.New("榜单 offset 应为 0 或 10 的倍数")
	}
	var cur Cursor
	if cursor != "" {
		decoded, ok := DecodeCursor(cursor)
		if !ok {
			return nil, errors.New("榜单游标无效，请从第一页重新加载")
		}
		cur = decoded
	}
	if offset > 0 && cur.SessionID == "" {
		return nil, errors.New("翻页缺少上一页游标，请从第一页重新加载")
	}
	tab := board.Tab
	var cellID string
	if plan, err := c.ensurePlan(ctx); err == nil && plan != nil {
		cellID = plan.CellID
		for _, b := range plan.Boards {
			if b.ID == board.ID {
				tab = b.Tab // plan taxonomy 优先于静态映射
			}
		}
	}
	if cellID == "" {
		cellID = defaultCellID
	}
	changeType := "2"
	if offset > 0 {
		changeType = "1"
	}
	params := cellChangeParams(cellID, board.ID, tab, offset, cur, changeType, c.identity)
	content, err := c.get(ctx, cellChangePath, params)
	if err != nil {
		return nil, err
	}
	return parseBoardResponse(content, offset)
}

// ensurePlan 惰性拉取 plan（进程内缓存 planTTL；失败短暂记忆避免打爆）。
func (c *Client) ensurePlan(ctx context.Context) (*planResult, error) {
	c.mu.Lock()
	cached := c.plan
	cachedAt := c.planAt
	c.mu.Unlock()
	ttl := c.nowTTL
	if ttl == 0 {
		ttl = planTTL
	}
	if cached != nil && time.Since(cachedAt) < ttl {
		return cached, nil
	}
	content, err := c.get(ctx, planPath, planParams(c.identity))
	if err != nil {
		return nil, err
	}
	result, err := parsePlanResponse(content)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.plan, c.planAt = result, time.Now()
	c.mu.Unlock()
	return result, nil
}

// get 发起一次 api3 GET（重试 + 业务码校验）。
func (c *Client) get(ctx context.Context, path string, params map[string]string) ([]byte, error) {
	base := c.BaseURL
	if base == "" {
		base = api3BaseURL
	}
	ua := c.UserAgent
	if ua == "" {
		ua = rankingsUserAgent
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(attempt) * time.Second):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+path+"?"+encodeQuery(refreshTicket(params)), nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("User-Agent", ua)
		request.Header.Set("Accept", "application/json")
		request.Header.Set("X-Xs-From-Web", "0")
		request.Header.Set("Sdk-Version", "2")
		request.Header.Set("Content-Type", "application/json; charset=utf-8")
		signRankings(request, nil, time.Now())
		response, err := client.Do(request)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		content, readErr := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
		response.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if response.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("红果榜单接口 HTTP %d", response.StatusCode)
			if response.StatusCode >= 400 && response.StatusCode < 500 {
				return nil, lastErr
			}
			continue
		}
		// 风控拦截形态：HTTP 200 + 空体（实测见包注释）。
		if len(content) == 0 || len(content) > maxBodyBytes {
			lastErr = errors.New("红果榜单接口未返回有效数据（疑似风控拦截）")
			continue
		}
		var envelope struct {
			Code    *json.Number `json:"code"`
			Message string       `json:"message"`
		}
		if err := json.Unmarshal(content, &envelope); err != nil || envelope.Code == nil {
			return nil, errors.New("红果榜单接口返回格式异常")
		}
		if code, _ := envelope.Code.Int64(); code != 0 {
			return nil, fmt.Errorf("红果榜单接口暂不可用（%s）", truncateRunes(firstNonEmpty(envelope.Message, envelope.Code.String()), 40))
		}
		return content, nil
	}
	return nil, lastErr
}
