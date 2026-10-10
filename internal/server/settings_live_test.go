package server

// 真机冒烟：GUO_LIVE=1 时对 scripts/jellyfin-test.sh 起的实例实测
// 设置保存验活两分支（正确 Key 200 保存 / 错误 Key 401 拒绝）。
// 实例地址/Key：GUO_JF_URL（默认 http://127.0.0.1:8096）+ GUO_JF_KEY
// （缺省读 /home/zhao/clone/guo/.jellyfin/apikey.txt，即脚本的产物）。

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestSettingsJellyfinLiveSmoke(t *testing.T) {
	if os.Getenv("GUO_LIVE") != "1" {
		t.Skip("GUO_LIVE=1 才跑真机冒烟（先 scripts/jellyfin-test.sh start）")
	}
	base := os.Getenv("GUO_JF_URL")
	if base == "" {
		base = "http://127.0.0.1:8096"
	}
	key := os.Getenv("GUO_JF_KEY")
	if key == "" {
		raw, err := os.ReadFile("/home/zhao/clone/guo/.jellyfin/apikey.txt")
		if err != nil {
			t.Skipf("未提供 GUO_JF_KEY 且读不到脚本产物 apikey.txt: %v", err)
		}
		key = strings.TrimSpace(string(raw))
	}

	clearSettingEnv(t)
	ts, st := newSettingsTestServer(t)

	// 分支 1：正确 Key → 验活 200，保存成功
	status, body := putSettings(t, ts.URL+"/api/v1/settings",
		fmt.Sprintf(`{"ass_export":true,"concurrency":2,"jellyfin":{"url":%q,"api_key":%q}}`, base, key))
	if status != http.StatusOK {
		t.Fatalf("正确 Key 应保存成功，status=%d body=%v", status, body)
	}
	if v, err := st.Setting(settingKeyJellyfinKey); err != nil || v != key {
		t.Errorf("Key 落库不符: %q err=%v", v, err)
	}
	t.Logf("验活 200 分支通过：jellyfin=%s", base)

	// 分支 2：错误 Key → 上游 401，400 拒绝且不落库
	status, body = putSettings(t, ts.URL+"/api/v1/settings",
		fmt.Sprintf(`{"ass_export":false,"concurrency":3,"jellyfin":{"url":%q,"api_key":"guo-live-wrong-key"}}`, base))
	if status != http.StatusBadRequest {
		t.Fatalf("错误 Key 应被拒绝，status=%d body=%v", status, body)
	}
	msg, _ := body["message"].(string)
	hint, _ := body["hint"].(string)
	t.Logf("验活 401 分支通过：%s / %s", msg, hint)
	// 拒绝请求不改写已保存值（验活失败整请求不落库）
	for key, want := range map[string]string{
		settingKeyASSExport:   "true",
		settingKeyConcurrency: "2",
	} {
		if v, err := st.Setting(key); err != nil || v != want {
			t.Errorf("拒绝后 %s = %q err=%v, 应保持 %q", key, v, err, want)
		}
	}

	// 存量 Key 不被错误请求破坏
	if v, err := st.Setting(settingKeyJellyfinKey); err != nil || v != key {
		t.Errorf("已存 Key 被破坏: %q err=%v", v, err)
	}
}
