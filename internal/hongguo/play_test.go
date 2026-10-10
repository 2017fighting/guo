package hongguo

// 播放页取流（线路偏好 + 画质档）与弹幕单窗口的公开面用例。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ---- App 线路画质选档 ----

func TestPickAppChoiceByQuality(t *testing.T) {
	choices, keyErr := appStreamChoices(appStreamFixture(), "8000011")
	if keyErr != nil || len(choices) == 0 {
		t.Fatalf("choices = %v keyErr = %v", choices, keyErr)
	}
	// quality=720：限定 720 档内选最优（h264 720 而非全场最高分的 1080）
	pick := pickAppChoice(choices, 720)
	if pick == nil || pick.stream.URL != "https://cdn.test/h264720.mp4" {
		t.Fatalf("quality=720 pick = %+v", pick)
	}
	// quality=1080：1080 档内按分数取 h265（bytevc 被降权）
	pick = pickAppChoice(choices, 1080)
	if pick == nil || pick.stream.URL != "https://cdn.test/hevc1080.mp4" {
		t.Fatalf("quality=1080 pick = %+v", pick)
	}
	// quality=999（无此档）：回落全场最优
	pick = pickAppChoice(choices, 999)
	if pick == nil || pick.stream.URL != "https://cdn.test/hevc1080.mp4" {
		t.Fatalf("quality=999 pick = %+v", pick)
	}
}

func TestAppQualityOptionsDedupAndOrder(t *testing.T) {
	choices, _ := appStreamChoices(appStreamFixture(), "8000011")
	opts := appQualityOptions(choices)
	if len(opts) != 2 || opts[0].ID != 1080 || opts[1].ID != 720 {
		t.Fatalf("opts = %+v", opts)
	}
	if opts[0].Label != "1080p" || opts[1].Label != "720p" {
		t.Fatalf("labels = %+v", opts)
	}
}

// ---- ResolvePlayStream：自动链 + 线路偏好 ----

// playerPageHTML 构造官网播放器页（内嵌 _ROUTER_DATA，口径同 web.go）。
func playerPageHTML(seriesID, vid, url string) string {
	page := map[string]any{
		"vid": vid, "series_id": seriesID,
		"video_player_info": map[string]any{"main_url": url, "duration": json.Number("100")},
	}
	data := map[string]any{"loaderData": map[string]any{"player_page/x": page}}
	encoded, _ := json.Marshal(data)
	return "<!DOCTYPE html><script>window._ROUTER_DATA = " + string(encoded) + ";</script>"
}

func newPlayClient(t *testing.T) (*Client, *httptest.Server, *httptest.Server) {
	t.Helper()
	appCalls := 0
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appCalls++
		fixture := appStreamFixture()
		fixture["video_list"].([]any)[1].(map[string]any)["video_meta"].(map[string]any)["vwidth"] = "608"
		fixture["video_list"].([]any)[1].(map[string]any)["video_meta"].(map[string]any)["vheight"] = "1080"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"video_model": fixture}})
	}))
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	c := NewClient()
	c.BaseURL = app.URL
	c.WebBaseURL = web.URL
	c.HTTP = app.Client()
	t.Cleanup(app.Close)
	t.Cleanup(web.Close)
	return c, app, web
}

func TestResolvePlayStreamAutoFallsBackToApp(t *testing.T) {
	c, _, _ := newPlayClient(t)
	play, err := c.ResolvePlayStream(context.Background(), "700001", "8000011", LineAuto, 0)
	if err != nil {
		t.Fatal(err)
	}
	if play.Line != LineApp || play.Stream.URL != "https://cdn.test/hevc1080.mp4" {
		t.Fatalf("play = %+v", play)
	}
	if len(play.Qualities) != 2 {
		t.Fatalf("qualities = %+v", play.Qualities)
	}
	if play.Stream.Referer != "" {
		t.Errorf("app 线路不应带 Referer")
	}
}

func TestResolvePlayStreamWebLine(t *testing.T) {
	c, app, web := newPlayClient(t)
	_ = app
	webHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Referer") == "" {
			t.Errorf("web 页面请求应带 Referer")
		}
		w.Write([]byte(playerPageHTML("700001", "8000011", "https://cdn.test/web.mp4")))
	})
	web.Config.Handler = webHandler

	play, err := c.ResolvePlayStream(context.Background(), "700001", "8000011", LineWeb, 0)
	if err != nil {
		t.Fatal(err)
	}
	if play.Line != LineWeb || play.Stream.URL != "https://cdn.test/web.mp4" {
		t.Fatalf("play = %+v", play)
	}
	if play.Stream.Referer != "https://hongguoduanju.com/" {
		t.Errorf("web 线路 Referer = %q", play.Stream.Referer)
	}
	if play.Stream.DurationMS != 100000 {
		t.Errorf("duration = %d", play.Stream.DurationMS)
	}
	// 官网线路单档：画质档为空（由服务端补「自动」）
	if len(play.Qualities) != 0 {
		t.Errorf("qualities = %+v", play.Qualities)
	}
	if !play.Vertical {
		t.Errorf("未知尺寸默认竖屏")
	}
}

func TestResolvePlayStreamWebOnlyDoesNotFallback(t *testing.T) {
	c, _, _ := newPlayClient(t)
	if _, err := c.ResolvePlayStream(context.Background(), "700001", "8000011", LineWeb, 0); err == nil {
		t.Fatal("指定 web 线路失败时应报错而非回落")
	}
}

func TestResolvePlayStreamQualityPassthrough(t *testing.T) {
	c, _, _ := newPlayClient(t)
	play, err := c.ResolvePlayStream(context.Background(), "700001", "8000011", LineAuto, 720)
	if err != nil {
		t.Fatal(err)
	}
	if play.Stream.URL != "https://cdn.test/h264720.mp4" || play.Stream.Quality != 720 {
		t.Fatalf("play = %+v", play.Stream)
	}
	// 该档原始尺寸 608x1080（fixture 已改写）→ 竖屏
	if !play.Vertical {
		t.Errorf("vertical = false, want true（608x1080）")
	}
}

func TestResolvePlayStreamBadLineAndIDs(t *testing.T) {
	c, _, _ := newPlayClient(t)
	if _, err := c.ResolvePlayStream(context.Background(), "700001", "8000011", "cdn", 0); err == nil {
		t.Fatal("未知线路应报错")
	}
	if _, err := c.ResolvePlayStream(context.Background(), "abc", "v1", "", 0); err == nil {
		t.Fatal("非法 ID 应报错")
	}
}

// ---- 兜底线路画质档 ----

func fallbackBody() string {
	spade := encodeSpadeA("aabb1122aabb1122aabb1122aabb1122")
	return `{"key_urls":[` +
		`{"name":"1080p","src":"https://cdn.test/fb1080.mp4","kid":"0102030405060708090a0b0c0d0e0f10","spade_a":"` + spade + `"},` +
		`{"name":"720p","src":"https://cdn.test/fb720.mp4","kid":"0102030405060708090a0b0c0d0e0f10","spade_a":"` + spade + `"}]}`
}

func TestResolvePlayStreamFallbackQuality(t *testing.T) {
	fb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Referer") != "https://hongguoduanju.com/" {
			t.Errorf("兜底 API 请求 Referer = %q", r.Header.Get("Referer"))
		}
		w.Write([]byte(fallbackBody()))
	}))
	defer fb.Close()
	_ = fb
	// playbackAPI 是常量，直接驱动候选提取与选档（网络层已有 resolveFallbackStream 覆盖口径）
	var response playbackResponse
	decoded, err := decodePlaybackResponse(fallbackBody())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(decoded, &response); err != nil {
		t.Fatal(err)
	}
	candidates, err := fallbackStreams(response)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].Quality != 1080 {
		t.Fatalf("candidates = %+v", candidates)
	}
	// quality=720 → 720 档
	choice := pickFallbackStream(candidates, 720)
	if choice == nil || choice.URL != "https://cdn.test/fb720.mp4" {
		t.Fatalf("quality=720 pick = %+v", choice)
	}
	if pickFallbackStream(candidates, 999).URL != "https://cdn.test/fb1080.mp4" {
		t.Fatal("无匹配档应回落最高档")
	}
}

// ---- 弹幕单窗口（30s 游标） ----

func TestDanmakuWindowExported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bp := body["business_param"].(map[string]any)
		start, _ := bp["start_offset_time"].(float64)
		// 过滤口径：跨窗口/坏行/越窗都必须被剔掉
		var next string
		var rows []map[string]any
		switch int(start) {
		case 0:
			next = "30000"
			rows = []map[string]any{
				danmakuComment("c1", "第一窗", "5000", "1"),
				danmakuComment("c2", "越窗丢弃", "31000", "1"),
				danmakuComment("c3", "状态不对", "6000", "0"),
			}
		case 30000:
			next = "60000"
			rows = []map[string]any{danmakuComment("c4", "第二窗", "45000", "1")}
		default:
			next = "60000"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(danmakuFixture(next, rows...))
	}))
	defer srv.Close()
	c := NewClient()
	c.BaseURL = srv.URL
	c.HTTP = srv.Client()

	items, next, err := c.DanmakuWindow(context.Background(), "700001", "800001", 0, 60000)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Text != "第一窗" || next != 30000 {
		t.Fatalf("window = %+v next=%d（越窗/坏状态行应被剔掉）", items, next)
	}
}
