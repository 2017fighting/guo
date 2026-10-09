package hongguo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	appBaseURL = "https://api5-normal-sinfonlineb.fqnovel.com"
	// UA 故意用老版本（70000/Android 12）：服务端疑似对最新 UA 全吐 bytevc（付费墙策略）。
	appUserAgent = "com.phoenix.read/70000 (Linux; U; Android 12; zh_CN; PFZM10; Build/SP1A.210812.016; Cronet/TTNetVersion:04270129 2024-01-15 QuicVersion:5e92d3a0 2023-08-23)"
	maxBodyBytes = 20 << 20
)

// Client 红果 App API 客户端（pipeline.Source 的实现载体）。
type Client struct {
	HTTP      *http.Client
	BaseURL   string // 测试注入用，默认线上地址
	UserAgent string // 默认 appUserAgent；可配置（协议文档建议）

	deviceID  string
	installID string
}

// NewClient 创建客户端，设备身份进程内固定。
func NewClient() *Client {
	return &Client{
		BaseURL:   appBaseURL,
		UserAgent: appUserAgent,
		deviceID:  newDeviceID(),
		installID: newDeviceID(),
	}
}

// commentMarker 旧版上下文标记已并入 appRequest 的 comment 参数。

// appRequest App API 通用请求（设备参数 + 签名 + 重试 + 业务码校验）。
// comment=true 时走弹幕（评论）签名全套（协议文档 §5.4）。
func (c *Client) appRequest(ctx context.Context, method, path string, extra url.Values, payload any, comment bool) (map[string]any, error) {
	base := c.BaseURL
	if base == "" {
		base = appBaseURL
	}
	ua := c.UserAgent
	if ua == "" {
		ua = appUserAgent
	}
	query := url.Values{
		"aid": {"8662"}, "app_name": {"novelread"}, "version_code": {"73532"}, "version_name": {"7.3.5.32"},
		"manifest_version_code": {"73532"}, "update_version_code": {"73532"}, "channel": {"update_64"},
		"device_platform": {"android"}, "os": {"android"}, "ssmix": {"a"}, "device_type": {"25053RT47C"},
		"device_brand": {"Redmi"}, "language": {"zh"}, "os_api": {"36"}, "os_version": {"16"},
		"resolution": {"1280*2772"}, "dpi": {"520"}, "ac": {"wifi"}, "device_id": {c.deviceID}, "iid": {c.installID},
	}
	for key, values := range extra {
		query[key] = append([]string(nil), values...)
	}
	var body []byte
	var err error
	if payload != nil {
		if body, err = json.Marshal(payload); err != nil {
			return nil, err
		}
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
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
		request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		request.Header.Set("User-Agent", ua)
		request.Header.Set("Accept", "application/json")
		request.Header.Set("X-XS-From-Web", "0")
		request.Header.Set("Sdk-Version", "2")
		var nonce commentNonce
		if comment {
			if nonce, err = newCommentNonce(); err != nil {
				return nil, errors.New("无法准备红果弹幕请求")
			}
			request.Header.Set("Comment-Source", "601")
			request.Header.Set("Server-Channel", "1000")
		}
		if payload != nil {
			request.Header.Set("Content-Type", "application/json; charset=utf-8")
		}
		now := time.Now()
		query.Set("_rticket", strconv.FormatInt(now.UnixMilli(), 10))
		request.URL.RawQuery = query.Encode()
		if comment {
			signCommentRequest(request, nonce, now)
		} else {
			signRequest(request, body, now)
		}
		response, err := httpClient.Do(request)
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
			lastErr = fmt.Errorf("红果 App 接口 HTTP %d", response.StatusCode)
			if response.StatusCode >= 400 && response.StatusCode < 500 {
				return nil, lastErr
			}
			continue
		}
		if len(content) == 0 || len(content) > maxBodyBytes {
			return nil, errors.New("红果 App 接口未返回有效数据")
		}
		var result map[string]any
		decoder := json.NewDecoder(bytes.NewReader(content))
		decoder.UseNumber()
		if err := decoder.Decode(&result); err != nil || result == nil {
			return nil, errors.New("红果 App 接口返回格式异常")
		}
		if code := firstNonEmpty(mapString(result, "code", "Code", "status_code"), mapString(nestedMap(result, "BaseResp"), "StatusCode")); code != "" && code != "0" {
			return nil, fmt.Errorf("红果 App 接口暂不可用（%s）", truncate(code, 20))
		}
		return result, nil
	}
	return nil, lastErr
}

// ---- 通用 JSON 取值助手（map[string]any 上的宽松取值，对齐 guoapp 口径） ----

func mapString(m map[string]any, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, key := range keys {
		if v, ok := m[key]; ok {
			switch value := v.(type) {
			case string:
				return value
			case json.Number:
				return value.String()
			case float64:
				return strconv.FormatFloat(value, 'f', -1, 64)
			case bool:
				return strconv.FormatBool(value)
			}
		}
	}
	return ""
}

func nestedMap(m map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		next, _ := m[key].(map[string]any)
		if next == nil {
			return map[string]any{}
		}
		m = next
	}
	return m
}

func anyList(v any) []any {
	list, _ := v.([]any)
	return list
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
