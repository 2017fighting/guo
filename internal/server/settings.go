package server

// 设置资源：GET/PUT /api/v1/settings（spec §4）。
// 字段落 SQLite settings 表（snake_case 键）；读取口径：表值优先，
// 环境变量兜底（GUO_ASS_EXPORT / GUO_CONCURRENCY / GUO_JELLYFIN_URL /
// GUO_PROXY_URL，与 CLI 启动口径一致）；api_key 永不回显。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/2017fighting/guo/internal/jellyfin"
)

// settingsKeys settings 表键名（新增键一律 snake_case）。
const (
	settingKeyASSExport    = "ass_export"
	settingKeyConcurrency  = "concurrency"
	settingKeyJellyfinURL  = "jellyfin_url"
	settingKeyJellyfinKey  = "jellyfin_api_key"
	settingKeyProxyURL     = "proxy_url"
	settingKeyProxyEnabled = "proxy_enabled"
)

// concurrencyBounds #7 决议：下载并发可配 1–6，默认 2。
const (
	concurrencyMin = 1
	concurrencyMax = 6
	concurrencyDef = 2
)

// SettingsStore 设置表读写（*store.Store 实现）。
type SettingsStore interface {
	Setting(key string) (string, error)
	SetSetting(key, value string) error
}

// Settings GET/PUT /api/v1/settings 的完整对象。
type Settings struct {
	AssExport   bool             `json:"ass_export"`
	Concurrency int              `json:"concurrency"`
	Jellyfin    JellyfinSettings `json:"jellyfin"`
	Proxy       ProxySettings    `json:"proxy"`
}

// JellyfinSettings 联动配置；api_key 只进不出（GET 恒为 ""，
// 是否已配置由 api_key_set 表达；PUT 传空 key 表示沿用已存 Key）。
type JellyfinSettings struct {
	URL       string `json:"url"`
	APIKey    string `json:"api_key"`
	APIKeySet bool   `json:"api_key_set"`
}

// ProxySettings 出站代理配置位：只存不生效（spec §11 预留），proxy_enabled 恒 false。
type ProxySettings struct {
	ProxyURL     string `json:"proxy_url"`
	ProxyEnabled bool   `json:"proxy_enabled"`
}

// LoadSettings 解析当前生效设置：settings 表优先，环境变量兜底。
// GET handler 与引擎热读取（cmd/guo serve 接 Engine.SettingsLookup）共用。
func LoadSettings(st SettingsStore) Settings {
	s := Settings{
		AssExport:   os.Getenv("GUO_ASS_EXPORT") != "0", // 默认开
		Concurrency: envConcurrency(),
	}
	s.Jellyfin.URL = os.Getenv("GUO_JELLYFIN_URL")
	// 预留位（spec §3 出站代理不做于本规格）：GUO_PROXY_URL 环境兑底；
	// proxy_enabled 仅认表值且 PUT 恒按关闭落库，无环境变量口径。
	s.Proxy.ProxyURL = os.Getenv("GUO_PROXY_URL")
	if v, err := st.Setting(settingKeyASSExport); err == nil {
		s.AssExport = v == "1" || v == "true" // 与 cmd/guo assExportSetting 同口径
	}
	if v, err := st.Setting(settingKeyConcurrency); err == nil {
		if n, perr := strconv.Atoi(v); perr == nil {
			s.Concurrency = clampConcurrency(n)
		}
	}
	if v, err := st.Setting(settingKeyJellyfinURL); err == nil {
		s.Jellyfin.URL = v
	}
	if v, err := st.Setting(settingKeyJellyfinKey); err == nil && v != "" {
		s.Jellyfin.APIKeySet = true
	}
	if v, err := st.Setting(settingKeyProxyURL); err == nil {
		s.Proxy.ProxyURL = v
	}
	return s
}

// envConcurrency GUO_CONCURRENCY 环境兜底（钳到 1–6，默认 2；非法值按默认）。
func envConcurrency() int {
	n := concurrencyDef
	if v := os.Getenv("GUO_CONCURRENCY"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			n = parsed
		}
	}
	return clampConcurrency(n)
}

func clampConcurrency(n int) int {
	if n < concurrencyMin {
		return concurrencyMin
	}
	if n > concurrencyMax {
		return concurrencyMax
	}
	return n
}

// handleSettingsGet GET /api/v1/settings —— 完整设置对象（api_key 掩码为空）。
func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	if s.Settings == nil {
		writeError(w, http.StatusServiceUnavailable, "设置存储未配置", "请检查服务启动日志")
		return
	}
	writeJSON(w, http.StatusOK, LoadSettings(s.Settings))
}

// settingsPutRequest PUT 请求体。ass_export / concurrency 必填；
// jellyfin / proxy 整段可省（省略 = 保持现状不动这两组键）。
type settingsPutRequest struct {
	AssExport   *bool               `json:"ass_export"`
	Concurrency *int                `json:"concurrency"`
	Jellyfin    *jellyfinPutRequest `json:"jellyfin"`
	Proxy       *proxyPutRequest    `json:"proxy"`
}

type jellyfinPutRequest struct {
	URL    string `json:"url"`
	APIKey string `json:"api_key"` // 空串 = 沿用已存 Key（GET 从不回显，表单空串重存即此意）
}

type proxyPutRequest struct {
	ProxyURL     string `json:"proxy_url"`
	ProxyEnabled bool   `json:"proxy_enabled"` // 预留位：只存不生效，恒按关闭落库
}

// handleSettingsPut PUT /api/v1/settings —— 全字段保存。
// Jellyfin 提供了地址时先 GET /System/Info 验活（200 才落库）；
// 验活失败整个请求都不写库。
func (s *Server) handleSettingsPut(w http.ResponseWriter, r *http.Request) {
	if s.Settings == nil {
		writeError(w, http.StatusServiceUnavailable, "设置存储未配置", "请检查服务启动日志")
		return
	}
	var req settingsPutRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "设置保存失败：请求体不是合法的 JSON 设置对象",
			"字段要求：ass_export 布尔、concurrency 整数 1–6、jellyfin/proxy 对象；"+err.Error())
		return
	}
	if req.AssExport == nil {
		writeError(w, http.StatusBadRequest, "设置保存失败：缺少 ass_export 字段",
			"弹幕导出开关是必填项，true 或 false")
		return
	}
	if req.Concurrency == nil {
		writeError(w, http.StatusBadRequest, "设置保存失败：缺少 concurrency 字段",
			"下载并发是必填项，取值 1–6")
		return
	}
	if *req.Concurrency < concurrencyMin || *req.Concurrency > concurrencyMax {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("下载并发 %d 不能用：允许范围是 1–6", *req.Concurrency),
			"并发指同时下载的剧集数，默认 2；改大前先确认带宽与源站限速（#7 决议）")
		return
	}

	// Jellyfin 分支：验活通过才决定落库内容（saveKey 即将写入的 Key 行）。
	var jfURL, saveKey string
	if req.Jellyfin != nil {
		jfURL = strings.TrimRight(req.Jellyfin.URL, "/")
		if jfURL == "" {
			saveKey = "" // 清空联动：URL 与 Key 一并清掉
		} else {
			if !strings.HasPrefix(jfURL, "http://") && !strings.HasPrefix(jfURL, "https://") {
				writeError(w, http.StatusBadRequest, "Jellyfin 地址不对：要以 http:// 或 https:// 开头",
					"例如 http://jellyfin:8096，填空则关闭联动")
				return
			}
			key := req.Jellyfin.APIKey
			if key == "" {
				if saved, err := s.Settings.Setting(settingKeyJellyfinKey); err == nil {
					key = saved
				}
			}
			if key == "" {
				writeError(w, http.StatusBadRequest, "Jellyfin 联动缺少 API Key",
					"要么补上 Key，要么清空地址先关闭联动")
				return
			}
			if err := s.validateJellyfin(r.Context(), jfURL, key); err != nil {
				writeRejection(w, err)
				return
			}
			saveKey = key
		}
	}

	st := s.Settings
	if err := st.SetSetting(settingKeyASSExport, strconv.FormatBool(*req.AssExport)); err != nil {
		writeError(w, http.StatusInternalServerError, "设置保存失败", err.Error())
		return
	}
	if err := st.SetSetting(settingKeyConcurrency, strconv.Itoa(*req.Concurrency)); err != nil {
		writeError(w, http.StatusInternalServerError, "设置保存失败", err.Error())
		return
	}
	if req.Jellyfin != nil {
		if err := st.SetSetting(settingKeyJellyfinURL, jfURL); err != nil {
			writeError(w, http.StatusInternalServerError, "设置保存失败", err.Error())
			return
		}
		if err := st.SetSetting(settingKeyJellyfinKey, saveKey); err != nil {
			writeError(w, http.StatusInternalServerError, "设置保存失败", err.Error())
			return
		}
	}
	if req.Proxy != nil {
		if err := st.SetSetting(settingKeyProxyURL, req.Proxy.ProxyURL); err != nil {
			writeError(w, http.StatusInternalServerError, "设置保存失败", err.Error())
			return
		}
		// 预留位：只存不生效，恒按关闭落库（spec §11 出站代理不做于本规格）
		if err := st.SetSetting(settingKeyProxyEnabled, "0"); err != nil {
			writeError(w, http.StatusInternalServerError, "设置保存失败", err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, LoadSettings(st))
}

// validateJellyfin 用提交的 URL+Key 调 GET /System/Info（10s 超时）。
func (s *Server) validateJellyfin(ctx context.Context, url, key string) error {
	jf := &jellyfin.Client{
		BaseURL: url,
		APIKey:  key,
		HTTP:    &http.Client{Timeout: 10 * time.Second},
	}
	return jf.Validate(ctx)
}

// writeRejection 把验活失败翻译成人话：Key 被拒（401/403）是调用方的问题，
// 连不上/上游异常按网关问题处理。
func writeRejection(w http.ResponseWriter, err error) {
	var se *jellyfin.StatusError
	switch {
	case errors.As(err, &se) && (se.Status == http.StatusUnauthorized || se.Status == http.StatusForbidden):
		writeError(w, http.StatusBadRequest, "Jellyfin 验活失败：这个 API Key 被拒绝了",
			fmt.Sprintf("上游 /System/Info 返回 %d；请到 Jellyfin 控制台重新生成 API Key 再保存", se.Status))
	case errors.As(err, &se):
		writeError(w, http.StatusBadGateway, "Jellyfin 验活失败：服务端返回异常状态",
			fmt.Sprintf("上游 /System/Info 返回 %d；请确认地址指向 Jellyfin 本体后重试", se.Status))
	default:
		writeError(w, http.StatusBadGateway, "Jellyfin 验活失败：连不上服务器",
			"请检查地址与网络是否可达："+err.Error())
	}
}
