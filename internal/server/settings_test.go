package server

// 设置 API 测试：GET/PUT /api/v1/settings 全字段往返、掩码、校验分支、
// Jellyfin 验活（httptest 假实例）。

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/2017fighting/guo/internal/hongguo"
	"github.com/2017fighting/guo/internal/store"
)

// newSettingsTestServer 起一个带真实 SQLite 设置表的测试服务。
func newSettingsTestServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := &Server{
		Catalog:  &fakeCatalog{page: &hongguo.CatalogPage{}},
		Settings: st,
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st
}

// clearSettingEnv 隔离环境变量兜底逻辑（空串按未设置处理）。
func clearSettingEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GUO_ASS_EXPORT", "")
	t.Setenv("GUO_CONCURRENCY", "")
	t.Setenv("GUO_JELLYFIN_URL", "")
	t.Setenv("GUO_PROXY_URL", "")
}

func mustSetting(t *testing.T, st *store.Store, key, value string) {
	t.Helper()
	if err := st.SetSetting(key, value); err != nil {
		t.Fatal(err)
	}
}

// ---- GET /api/v1/settings ----

func TestSettingsGetDefaults(t *testing.T) {
	clearSettingEnv(t)
	ts, _ := newSettingsTestServer(t)

	resp, err := http.Get(ts.URL + "/api/v1/settings")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decodeJSON(t, resp)
	if body["ass_export"] != true {
		t.Errorf("ass_export 默认应为开: %v", body["ass_export"])
	}
	if body["concurrency"] != float64(2) {
		t.Errorf("concurrency 默认应为 2: %v", body["concurrency"])
	}
	jf, _ := body["jellyfin"].(map[string]any)
	if jf == nil || jf["url"] != "" || jf["api_key"] != "" || jf["api_key_set"] != false {
		t.Errorf("jellyfin 默认应为空: %v", jf)
	}
	proxy, _ := body["proxy"].(map[string]any)
	if proxy == nil || proxy["proxy_url"] != "" || proxy["proxy_enabled"] != false {
		t.Errorf("proxy 默认应为空: %v", proxy)
	}
}

func TestSettingsGetEnvFallback(t *testing.T) {
	t.Setenv("GUO_ASS_EXPORT", "0")
	t.Setenv("GUO_CONCURRENCY", "4")
	t.Setenv("GUO_JELLYFIN_URL", "http://env-jf:8096")
	t.Setenv("GUO_PROXY_URL", "http://env-proxy:7890")
	ts, _ := newSettingsTestServer(t)

	resp, err := http.Get(ts.URL + "/api/v1/settings")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := decodeJSON(t, resp)
	if body["ass_export"] != false {
		t.Errorf("GUO_ASS_EXPORT=0 应关闭: %v", body["ass_export"])
	}
	if body["concurrency"] != float64(4) {
		t.Errorf("GUO_CONCURRENCY=4 应生效: %v", body["concurrency"])
	}
	jf, _ := body["jellyfin"].(map[string]any)
	if jf["url"] != "http://env-jf:8096" {
		t.Errorf("GUO_JELLYFIN_URL 应兜底: %v", jf)
	}
	// 预留位：GUO_PROXY_URL 环境兜底，proxy_enabled 恒关（仅认表值）。
	proxy, _ := body["proxy"].(map[string]any)
	if proxy["proxy_url"] != "http://env-proxy:7890" {
		t.Errorf("GUO_PROXY_URL 应兜底: %v", proxy)
	}
	if proxy["proxy_enabled"] != false {
		t.Errorf("proxy_enabled 应恒关: %v", proxy)
	}
}

func putSettings(t *testing.T, url string, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode, decodeJSON(t, resp)
}

// ---- PUT /api/v1/settings：全字段往返 ----

func TestSettingsPutRoundTrip(t *testing.T) {
	clearSettingEnv(t)
	ts, st := newSettingsTestServer(t)

	status, body := putSettings(t, ts.URL+"/api/v1/settings", `{"ass_export":false,"concurrency":3,"proxy":{"proxy_url":"http://proxy:7890","proxy_enabled":true}}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%v", status, body)
	}
	if body["ass_export"] != false || body["concurrency"] != float64(3) {
		t.Errorf("回显不符: %v", body)
	}
	proxy, _ := body["proxy"].(map[string]any)
	if proxy["proxy_url"] != "http://proxy:7890" {
		t.Errorf("proxy_url 往返失真: %v", proxy)
	}
	if proxy["proxy_enabled"] != false {
		t.Errorf("proxy_enabled 预留位必须恒为 false: %v", proxy)
	}

	// GET 再读一遍，确认落库后的完整往返
	resp, err := http.Get(ts.URL + "/api/v1/settings")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got := decodeJSON(t, resp)
	if got["ass_export"] != false || got["concurrency"] != float64(3) {
		t.Errorf("GET 往返失真: %v", got)
	}
	gp, _ := got["proxy"].(map[string]any)
	if gp["proxy_url"] != "http://proxy:7890" || gp["proxy_enabled"] != false {
		t.Errorf("proxy GET 往返失真: %v", gp)
	}
	// 落 SQLite settings 表（键名即契约）
	for key, want := range map[string]string{
		settingKeyASSExport:    "false",
		settingKeyConcurrency:  "3",
		settingKeyProxyURL:     "http://proxy:7890",
		settingKeyProxyEnabled: "0",
	} {
		if v, err := st.Setting(key); err != nil || v != want {
			t.Errorf("settings[%s] = %q err=%v, want %q", key, v, err, want)
		}
	}
}

func TestSettingsPutValidation(t *testing.T) {
	clearSettingEnv(t)
	cases := []struct {
		name    string
		body    string
		message string
	}{
		{"并发 0", `{"ass_export":true,"concurrency":0}`, "并发"},
		{"并发 7", `{"ass_export":true,"concurrency":7}`, "并发"},
		{"缺并发", `{"ass_export":true}`, "concurrency"},
		{"缺开关", `{"concurrency":2}`, "ass_export"},
		{"坏 JSON", `{not-json`, "JSON"},
		{"并发类型错", `{"ass_export":true,"concurrency":"three"}`, "JSON"},
		{"开关类型错", `{"ass_export":"yes","concurrency":2}`, "JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts, st := newSettingsTestServer(t)
			status, body := putSettings(t, ts.URL+"/api/v1/settings", tc.body)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d body=%v", status, body)
			}
			msg, _ := body["message"].(string)
			if msg == "" || !strings.Contains(msg, tc.message) {
				t.Errorf("message = %q, 应含 %q", msg, tc.message)
			}
			if hint, _ := body["hint"].(string); hint == "" {
				t.Errorf("hint 缺失: %v", body)
			}
			// 拒绝时一个键都不落库
			if v, err := st.Setting(settingKeyConcurrency); err == nil {
				t.Errorf("拒绝时不应写库，但 concurrency=%q", v)
			}
		})
	}
}

func TestSettingsGetMaskAPIKeyAndRowPrecedence(t *testing.T) {
	clearSettingEnv(t)
	ts, st := newSettingsTestServer(t)
	mustSetting(t, st, "ass_export", "0")
	mustSetting(t, st, "concurrency", "5")
	mustSetting(t, st, "jellyfin_url", "http://jf:8096")
	mustSetting(t, st, "jellyfin_api_key", "SECRET-KEY")

	resp, err := http.Get(ts.URL + "/api/v1/settings")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := decodeJSON(t, resp)
	if body["ass_export"] != false || body["concurrency"] != float64(5) {
		t.Errorf("settings 表应优先于环境变量: %v", body)
	}
	jf, _ := body["jellyfin"].(map[string]any)
	if jf["url"] != "http://jf:8096" {
		t.Errorf("jellyfin.url = %v", jf)
	}
	if jf["api_key"] != "" {
		t.Errorf("api_key 绝不能回显: %q", jf["api_key"])
	}
	if jf["api_key_set"] != true {
		t.Errorf("api_key_set 应为 true: %v", jf)
	}
}

// ---- PUT /api/v1/settings：Jellyfin 验活分支 ----

// newJellyfinFake 假 Jellyfin：GET /System/Info，Token 等于 validKey 才 200，
// 其余 401；记录命中数与最近一次 Authorization 头。
func newJellyfinFake(t *testing.T, validKey string) (*httptest.Server, *int32, *string) {
	t.Helper()
	var hits int32
	var lastAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/System/Info" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		lastAuth = r.Header.Get("Authorization")
		if lastAuth != fmt.Sprintf("MediaBrowser Token=%q", validKey) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ServerName":"fake-jf","Version":"10.11.0"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits, &lastAuth
}

func TestSettingsPutJellyfinValidateOK(t *testing.T) {
	clearSettingEnv(t)
	ts, st := newSettingsTestServer(t)
	jf, hits, lastAuth := newJellyfinFake(t, "good-key")

	status, body := putSettings(t, ts.URL+"/api/v1/settings",
		fmt.Sprintf(`{"ass_export":true,"concurrency":2,"jellyfin":{"url":%q,"api_key":"good-key"}}`, jf.URL))
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%v", status, body)
	}
	jb, _ := body["jellyfin"].(map[string]any)
	if jb["url"] != jf.URL || jb["api_key"] != "" || jb["api_key_set"] != true {
		t.Errorf("jellyfin 回显不符（api_key 必须掩码）: %v", jb)
	}
	if atomic.LoadInt32(hits) != 1 {
		t.Errorf("验活应恰好请求一次 /System/Info，hits=%d", hits)
	}
	if *lastAuth != `MediaBrowser Token="good-key"` {
		t.Errorf("验活鉴权头 = %q", *lastAuth)
	}
	if v, err := st.Setting(settingKeyJellyfinKey); err != nil || v != "good-key" {
		t.Errorf("jellyfin_api_key 落库 = %q err=%v", v, err)
	}
}

func TestSettingsPutJellyfinWrongKeyRejected(t *testing.T) {
	clearSettingEnv(t)
	ts, st := newSettingsTestServer(t)
	jf, _, _ := newJellyfinFake(t, "good-key")

	status, body := putSettings(t, ts.URL+"/api/v1/settings",
		fmt.Sprintf(`{"ass_export":true,"concurrency":2,"jellyfin":{"url":%q,"api_key":"bad-key"}}`, jf.URL))
	if status != http.StatusBadRequest {
		t.Fatalf("401 应以 400 拒绝，status = %d body=%v", status, body)
	}
	msg, _ := body["message"].(string)
	if !strings.Contains(msg, "Key") && !strings.Contains(msg, "验活") {
		t.Errorf("message = %q 应讲清 Key 被拒", msg)
	}
	hint, _ := body["hint"].(string)
	if !strings.Contains(hint, "401") {
		t.Errorf("hint = %q 应带上游状态 401", hint)
	}
	// 验活失败：整个请求都不落库
	for _, key := range []string{settingKeyASSExport, settingKeyConcurrency, settingKeyJellyfinURL, settingKeyJellyfinKey} {
		if _, err := st.Setting(key); err == nil {
			t.Errorf("拒绝时不应写库，但 %s 已写入", key)
		}
	}
}

func TestSettingsPutJellyfinUnreachableRejected(t *testing.T) {
	clearSettingEnv(t)
	ts, st := newSettingsTestServer(t)
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close() // 立刻关掉模拟连不上

	status, body := putSettings(t, ts.URL+"/api/v1/settings",
		fmt.Sprintf(`{"ass_export":true,"concurrency":2,"jellyfin":{"url":%q,"api_key":"k"}}`, deadURL))
	if status != http.StatusBadGateway {
		t.Fatalf("连不上应以 502 拒绝，status = %d body=%v", status, body)
	}
	msg, _ := body["message"].(string)
	if !strings.Contains(msg, "连不上") && !strings.Contains(msg, "验活") {
		t.Errorf("message = %q", msg)
	}
	if _, err := st.Setting(settingKeyJellyfinURL); err == nil {
		t.Errorf("拒绝时不应写库")
	}
}

func TestSettingsPutJellyfinServer5xxRejected(t *testing.T) {
	clearSettingEnv(t)
	ts, _ := newSettingsTestServer(t)
	var status int32 = 500
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(atomic.LoadInt32(&status)))
	}))
	t.Cleanup(srv.Close)

	code, body := putSettings(t, ts.URL+"/api/v1/settings",
		fmt.Sprintf(`{"ass_export":true,"concurrency":2,"jellyfin":{"url":%q,"api_key":"k"}}`, srv.URL))
	if code != http.StatusBadGateway {
		t.Fatalf("上游 5xx 应以 502 拒绝，status = %d body=%v", code, body)
	}
}

func TestSettingsPutJellyfinMaskedResaveKeepsKey(t *testing.T) {
	clearSettingEnv(t)
	ts, st := newSettingsTestServer(t)
	jf, hits, _ := newJellyfinFake(t, "saved-key")
	mustSetting(t, st, settingKeyJellyfinURL, "http://old-jf:8096")
	mustSetting(t, st, settingKeyJellyfinKey, "saved-key")

	// 表单回读时 api_key 恒为空串；只换 URL 重存 → 用已存 Key 验活
	status, body := putSettings(t, ts.URL+"/api/v1/settings",
		fmt.Sprintf(`{"ass_export":true,"concurrency":2,"jellyfin":{"url":%q,"api_key":""}}`, jf.URL))
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%v", status, body)
	}
	if atomic.LoadInt32(hits) != 1 {
		t.Errorf("应沿用已存 Key 验活，hits=%d", hits)
	}
	if v, err := st.Setting(settingKeyJellyfinKey); err != nil || v != "saved-key" {
		t.Errorf("已存 Key 不应被清掉: %q err=%v", v, err)
	}
	jb, _ := body["jellyfin"].(map[string]any)
	if jb["api_key_set"] != true || jb["url"] != jf.URL {
		t.Errorf("jellyfin 回显不符: %v", jb)
	}
}

func TestSettingsPutJellyfinMissingKeyRejected(t *testing.T) {
	clearSettingEnv(t)
	ts, _ := newSettingsTestServer(t)
	jf, hits, _ := newJellyfinFake(t, "good-key")

	status, body := putSettings(t, ts.URL+"/api/v1/settings",
		fmt.Sprintf(`{"ass_export":true,"concurrency":2,"jellyfin":{"url":%q,"api_key":""}}`, jf.URL))
	if status != http.StatusBadRequest {
		t.Fatalf("有地址无 Key 应 400，status = %d body=%v", status, body)
	}
	if atomic.LoadInt32(hits) != 0 {
		t.Errorf("没有 Key 不应发起验活")
	}
}

func TestSettingsPutJellyfinClearDisables(t *testing.T) {
	clearSettingEnv(t)
	ts, st := newSettingsTestServer(t)
	jf, hits, _ := newJellyfinFake(t, "saved-key")
	mustSetting(t, st, settingKeyJellyfinURL, jf.URL)
	mustSetting(t, st, settingKeyJellyfinKey, "saved-key")

	status, body := putSettings(t, ts.URL+"/api/v1/settings",
		`{"ass_export":true,"concurrency":2,"jellyfin":{"url":"","api_key":""}}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%v", status, body)
	}
	if atomic.LoadInt32(hits) != 0 {
		t.Errorf("清空联动不应发起验活")
	}
	if v, err := st.Setting(settingKeyJellyfinURL); err != nil || v != "" {
		t.Errorf("清空后 url 行应为空串: %q err=%v", v, err)
	}
	jb, _ := body["jellyfin"].(map[string]any)
	if jb["api_key_set"] != false {
		t.Errorf("清空后 api_key_set 应为 false: %v", jb)
	}
}

func TestSettingsPutJellyfinBadSchemeRejected(t *testing.T) {
	clearSettingEnv(t)
	ts, _ := newSettingsTestServer(t)
	status, body := putSettings(t, ts.URL+"/api/v1/settings",
		`{"ass_export":true,"concurrency":2,"jellyfin":{"url":"ftp://jf:8096","api_key":"k"}}`)
	if status != http.StatusBadRequest {
		t.Fatalf("非 http(s) 地址应 400，status = %d body=%v", status, body)
	}
	if msg, _ := body["message"].(string); !strings.Contains(msg, "地址") {
		t.Errorf("message = %q", msg)
	}
}
