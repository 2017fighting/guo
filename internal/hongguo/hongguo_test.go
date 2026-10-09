package hongguo

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math/bits"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2017fighting/guo/internal/ass"
)

// ---- 签名 ----

func TestSM3StandardVector(t *testing.T) {
	// GB/T 32905 标准向量：sm3("abc")
	got := sm3([]byte("abc"))
	want := "66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0"
	if hex.EncodeToString(got[:]) != want {
		t.Errorf("sm3(abc) = %x, want %s", got, want)
	}
}

func TestSignRequestShape(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "https://x.test/api?a=1", strings.NewReader("body"))
	body := []byte(`{"k":1}`)
	now := time.Unix(1750000000, 0)
	signRequest(req, body, now)
	g := req.Header.Get("X-Gorgon")
	if len(g) != 52 || !strings.HasPrefix(g, "840440") { // 6 头 + 20 载荷字节
		t.Errorf("X-Gorgon = %q", g)
	}
	if req.Header.Get("X-Khronos") != "1750000000" {
		t.Errorf("X-Khronos = %q", req.Header.Get("X-Khronos"))
	}
	if req.Header.Get("X-SS-Req-Ticket") == "" {
		t.Error("X-SS-Req-Ticket missing")
	}
	if req.Header.Get("X-SS-STUB") != strings.ToUpper(req.Header.Get("X-SS-STUB")) {
		t.Errorf("X-SS-STUB should be uppercase md5: %q", req.Header.Get("X-SS-STUB"))
	}
}

func TestSignCommentDeterministic(t *testing.T) {
	build := func() http.Header {
		req, _ := http.NewRequest(http.MethodPost, "https://x.test/api?aid=8662&b=2", nil)
		nonce := commentNonce{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
		signCommentRequest(req, nonce, time.Unix(1750000000, 0))
		return req.Header
	}
	h1, h2 := build(), build()
	for _, key := range []string{"X-Gorgon", "X-Argus", "X-Ladon"} {
		if h1.Get(key) == "" {
			t.Fatalf("%s missing", key)
		}
		if h1.Get(key) != h2.Get(key) {
			t.Errorf("%s not deterministic", key)
		}
	}
	if h1.Get("X-Gorgon") == "" || len(h1.Get("X-Gorgon")) != 52 {
		t.Errorf("comment X-Gorgon shape: %q", h1.Get("X-Gorgon"))
	}
}

// ---- spade_a 密钥提取（前向编码做往返验证） ----

// encodeSpadeA 是 contentKey 的逆过程，仅测试用。
func encodeSpadeA(keyHex string) string {
	content := "0" + keyHex // padding=0
	contentLength := len(content)
	tag := "app"
	tagLength := len(tag)
	seed := byte(0x5a)

	raw := make([]byte, 0, 1+contentLength+tagLength)
	// content 编码：current = (decoded + 21 + popcount(i)) ^ previous
	previousEven, previousOdd := byte(250), byte(85)
	var contentBytes []byte
	for index := 0; index < contentLength; index++ {
		previous := previousOdd
		if index%2 == 0 {
			previous = previousEven
		}
		current := byte(int(content[index])+21+bits.OnesCount(uint(index))) ^ previous
		if index%2 == 0 {
			previousEven = current
		} else {
			previousOdd = current
		}
		contentBytes = append(contentBytes, current)
	}
	raw = append(raw, 0)
	raw[0] = byte(48+tagLength) ^ contentBytes[0] ^ contentBytes[1]
	raw = append(raw, contentBytes...)
	for _, b := range []byte(tag) {
		raw = append(raw, b^seed)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func TestContentKeyRoundTrip(t *testing.T) {
	keyHex := "0123456789abcdef0123456789abcdef"
	spade := encodeSpadeA(keyHex)
	got, err := contentKey(spade)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got) != keyHex {
		t.Errorf("got %x, want %s", got, keyHex)
	}
}

func TestContentKeyRejectsV2Tag(t *testing.T) {
	// tag 首字节由解码器种子推导强等于内容末字节：选 'a' 结尾的密钥使 tag 能拼出 "app_v2"
	content := "0" + strings.Repeat("ab", 15) + "aa" // 末字节 'a'
	if content[len(content)-1] != 'a' {
		t.Fatal("fixture broken")
	}
	contentLength := len(content)
	tag := "app_v2"
	tagLength := len(tag)

	contentBytes := []byte(content)
	previousEven, previousOdd := byte(250), byte(85)
	for index := 0; index < contentLength; index++ {
		previous := previousOdd
		if index%2 == 0 {
			previous = previousEven
		}
		current := byte(int(content[index])+21+bits.OnesCount(uint(index))) ^ previous
		if index%2 == 0 {
			previousEven = current
		} else {
			previousOdd = current
		}
		contentBytes[index] = current
	}
	// 解码器的种子 = 编码后内容末两字节 XOR；tag[0] 由此推导，其余 tag 字节用它异或写入
	seed := contentBytes[contentLength-2] ^ contentBytes[contentLength-1]
	raw := append([]byte{0}, contentBytes...)
	raw[0] = byte(48+tagLength) ^ contentBytes[0] ^ contentBytes[1]
	raw = append(raw, content[contentLength-1]^seed)
	for _, b := range []byte(tag[1:]) {
		raw = append(raw, b^seed)
	}
	if _, err := contentKey(base64.StdEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("app_v2 tag should be rejected")
	}
}

// ---- v2 兜底信封（前向加密做往返验证） ----

func encodeV2Envelope(plain []byte) string {
	material := make([]byte, 32)
	for i := range material {
		material[i] = byte(i * 7)
	}
	block, _ := aes.NewCipher(material[:16])
	padded := commentPad(plain)
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, material[16:32]).CryptBlocks(ciphertext, padded)

	mask := [...]byte{104, 64, 70, 166, 190, 168, 143, 130, 225, 254, 251, 217, 196, 34, 45, 60, 29, 20, 103, 105}
	encoded := make([]byte, 32)
	for index, m := range material {
		previous := byte(109)
		if index > 0 {
			previous = encoded[index-1]
		}
		slot := index % len(mask)
		salt := mask[slot] ^ byte(90+13*slot) ^ 85
		rotated := m ^ previous ^ salt
		shifted := bits.RotateLeft8(rotated, -3) // rotr(rotl(s,3)) = s
		encoded[index] = byte(int(shifted) - 215 + 11*index)
	}
	return "v2.abcd" + hex.EncodeToString(encoded) + "." + base64.StdEncoding.EncodeToString(ciphertext)
}

func TestDecodePlaybackResponseRoundTrip(t *testing.T) {
	plain := []byte(`{"parse":null,"jx":null,"key_urls":[]}`)
	envelope := encodeV2Envelope(plain)
	got, err := decodePlaybackResponse(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Errorf("got %s", got)
	}
	// 明文直接透传
	got, err = decodePlaybackResponse(`{"ok":1}`)
	if err != nil || string(got) != `{"ok":1}` {
		t.Errorf("plain passthrough failed: %s %v", got, err)
	}
}

// ---- 选档策略 ----

func appStreamFixture() map[string]any {
	spade := encodeSpadeA("aabb1122aabb1122aabb1122aabb1122")
	return map[string]any{
		"video_duration": json.Number("120"),
		"video_list": []any{
			map[string]any{
				"main_url": "https://cdn.test/hevc1080.mp4",
				"video_meta": map[string]any{
					"codec_type": "h265_hvc1", "vheight": "1080", "vwidth": "1920", "definition": "1080p",
				},
				"encrypt_info": map[string]any{
					"spade_a": spade, "encrypt": true, "encryption_method": "cenc-aes-ctr",
				},
			},
			map[string]any{
				"main_url": base64.StdEncoding.EncodeToString([]byte("https://cdn.test/h264720.mp4")),
				"video_meta": map[string]any{
					"codec_type": "h264", "vheight": "720", "vwidth": "1280", "definition": "720p",
				},
			},
			map[string]any{
				"main_url": "https://cdn.test/bytevc1080.mp4",
				"video_meta": map[string]any{
					"codec_type": "bytevc2", "vheight": "1080", "vwidth": "1920", "definition": "1080p",
				},
			},
		},
	}
}

func TestSelectAppStreamPrefersPlayableOverBytevc(t *testing.T) {
	stream, err := selectAppStream(appStreamFixture(), "v1")
	if err != nil {
		t.Fatal(err)
	}
	// hevc 1080 (score 10800) > h264 720 (7201) > bytevc 1080 (-89200)
	if stream.URL != "https://cdn.test/hevc1080.mp4" {
		t.Errorf("selected %q", stream.URL)
	}
	if stream.CENCKeyHex != "aabb1122aabb1122aabb1122aabb1122" {
		t.Errorf("cenc key = %q", stream.CENCKeyHex)
	}
	if stream.DurationMS != 120000 {
		t.Errorf("duration = %d", stream.DurationMS)
	}
	if stream.Referer != "" {
		t.Errorf("app line must not carry Referer")
	}
}

func TestSelectAppStreamBytevcOnlyStillSelectable(t *testing.T) {
	fixture := map[string]any{"video_list": []any{
		map[string]any{
			"main_url": "https://cdn.test/bv.mp4",
			"video_meta": map[string]any{
				"codec_type": "bytevc1", "vheight": "720", "definition": "720p",
			},
		},
	}}
	stream, err := selectAppStream(fixture, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if stream.URL != "https://cdn.test/bv.mp4" || stream.Quality != 0 {
		t.Errorf("bytevc fallback: %+v", stream)
	}
}

// ---- 弹幕解析与窗口循环 ----

func danmakuFixture(next string, items ...map[string]any) map[string]any {
	rows := []any{}
	for _, it := range items {
		rows = append(rows, map[string]any{"comment": it})
	}
	return map[string]any{
		"code": 0,
		"data": map[string]any{
			"data_list":        rows,
			"extra":            map[string]any{"next_query_danmaku_list_time": next},
			"common_list_info": map[string]any{"cursor": `{"danmaku_count":250}`, "has_more": false},
		},
	}
}

func danmakuComment(id, text, offset, status string) map[string]any {
	return map[string]any{
		"comment_id": id,
		"common": map[string]any{
			"group_id": "800001", "status": status,
			"content": map[string]any{"text": text},
		},
		"expand": map[string]any{"offset_time": offset},
	}
}

func TestParseDanmakuPage(t *testing.T) {
	page := danmakuFixture("30000",
		danmakuComment("c1", "第一句", "1500", "1"),
		danmakuComment("c2", "隐藏的", "2000", "2"),      // status != 1 → 丢弃
		danmakuComment("c3", "越界的", "31000", "1"),     // 超出窗口 → 丢弃
		danmakuComment("c4", "控制\x01符", "16000", "1"), // 净化
	)
	result, err := parseDanmakuPage(page, "800001", 0, 60000)
	if err != nil {
		t.Fatal(err)
	}
	if result.nextMS != 30000 {
		t.Errorf("next = %d", result.nextMS)
	}
	if len(result.items) != 2 {
		t.Fatalf("items = %+v", result.items)
	}
	if result.items[0].ID != "c1" || result.items[0].TimeMS != 1500 {
		t.Errorf("first = %+v", result.items[0])
	}
	if strings.ContainsRune(result.items[1].Text, 1) || result.items[1].Text != "控制 符" {
		t.Errorf("sanitize failed: %q", result.items[1].Text)
	}
}

func TestDanmakuAllWindows(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.Header.Get("Comment-Source") != "601" || r.Header.Get("X-Argus") == "" {
			t.Errorf("comment signing headers missing: %v", r.Header)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		bp := body["business_param"].(map[string]any)
		start, _ := bp["start_offset_time"].(float64)
		var page map[string]any
		switch int(start) {
		case 0:
			page = danmakuFixture("30000", danmakuComment("a", "早", "5000", "1"))
		case 30000:
			page = danmakuFixture("60000", danmakuComment("b", "晚", "40000", "1"), danmakuComment("a", "重复", "31000", "1"))
		default:
			t.Errorf("unexpected start %v", start)
		}
		json.NewEncoder(w).Encode(page)
	}))
	defer srv.Close()

	c := NewClient()
	c.BaseURL = srv.URL
	c.HTTP = srv.Client()
	items, err := c.DanmakuAll(context.Background(), "700001", "800001", 55000)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != "a" || items[1].ID != "b" {
		t.Fatalf("items = %+v", items)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("window calls = %d", calls)
	}
}

// ---- 详情解析（走完整 appRequest 管线） ----

func TestDetailViaAppRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/novel/player/video_detail/v1/" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("X-Gorgon") == "" || r.Header.Get("User-Agent") != appUserAgent {
			t.Errorf("signing headers missing")
		}
		if r.URL.Query().Get("device_id") == "" || r.URL.Query().Get("aid") != "8662" {
			t.Errorf("device query missing")
		}
		json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{
				"video_data": map[string]any{
					"series_id_str":      "700001",
					"series_title":       "测试剧",
					"series_intro":       "简介",
					"series_cover":       "//p.test/cover.jpg",
					"episode_cnt":        "2",
					"tags":               []any{"都市", "甜宠"},
					"first_visible_time": "1735689600000",
					"video_list": []any{
						map[string]any{"vid": "800001", "vid_index": "1", "series_id": "700001"},
						map[string]any{"vid": "800002", "vid_index": "2", "series_id": "700001"},
					},
				},
			},
		})
	}))
	defer srv.Close()

	c := NewClient()
	c.BaseURL = srv.URL
	c.HTTP = srv.Client()
	meta, err := c.Detail(context.Background(), "700001")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != "测试剧" || len(meta.Episodes) != 2 || meta.Episodes[1].VID != "800002" {
		t.Fatalf("meta = %+v", meta)
	}
	if meta.CoverURL != "https://p.test/cover.jpg" {
		t.Errorf("cover = %q", meta.CoverURL)
	}
	if meta.Year != "2025" {
		t.Errorf("year = %q", meta.Year)
	}
	if len(meta.Genres) != 2 {
		t.Errorf("genres = %v", meta.Genres)
	}
}

func TestDetailRejectsIncompleteEpisodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{
				"video_data": map[string]any{
					"series_id_str": "700001", "series_title": "t", "episode_cnt": "3",
					"video_list": []any{
						map[string]any{"vid": "800001", "vid_index": "1"},
					},
				},
			},
		})
	}))
	defer srv.Close()
	c := NewClient()
	c.BaseURL = srv.URL
	c.HTTP = srv.Client()
	if _, err := c.Detail(context.Background(), "700001"); err == nil {
		t.Fatal("incomplete episode list should be rejected")
	}
}

// ---- 弹幕 Comment 类型与 ass 包对齐的编译期检查 ----

var _ []ass.Comment
