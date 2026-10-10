// Package jellyfin 提供「下载完成后触发库刷新」的最小客户端。
//
// 依据 docs/research/jellyfin-integration.md 第 5 节：
//   - 鉴权只用 Authorization: MediaBrowser Token="<API_KEY>"（legacy 头/参数将移除）
//   - 验活 GET /System/Info；刷新 POST /Library/Refresh（204，服务端跑完库刷新才返回，
//     客户端须设分钟级超时并异步触发；失败只记日志不阻断下载流程）
package jellyfin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Client 是可选集成的 Jellyfin 服务端客户端；BaseURL 或 APIKey 为空时所有方法为空操作。
type Client struct {
	BaseURL string // 如 http://jellyfin:8096
	APIKey  string

	HTTP    *http.Client
	Timeout time.Duration // 刷新超时，默认 10 分钟（服务端扫描完成才返回）
	// OnError 非空时接收刷新失败信息（用于日志）；刷新永不返回错误。
	OnError func(msg string)
}

// Enabled 报告是否已配置生效。
func (c *Client) Enabled() bool { return c.BaseURL != "" && c.APIKey != "" }

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return 10 * time.Minute
}

// Validate 验证 URL + API Key（GET /System/Info），用于配置保存时验活。
// 非 200 时返回 *StatusError（携带上游状态码，供设置接口给出人话提示）。
func (c *Client) Validate(ctx context.Context) error {
	if !c.Enabled() {
		return errors.New("jellyfin: not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/System/Info", nil)
	if err != nil {
		return err
	}
	c.auth(req)
	resp, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &StatusError{Status: resp.StatusCode}
	}
	return nil
}

// StatusError /System/Info 返回非 200 时携带上游状态码。
type StatusError struct{ Status int }

func (e *StatusError) Error() string { return fmt.Sprintf("jellyfin: /System/Info status %d", e.Status) }

// RefreshAsync 异步触发全库刷新（POST /Library/Refresh），立即返回。
// 未配置时为空操作；失败只上报 OnError。
func (c *Client) RefreshAsync() {
	if !c.Enabled() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), c.timeout())
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/Library/Refresh", nil)
		if err != nil {
			c.report("构建请求失败: " + err.Error())
			return
		}
		c.auth(req)
		resp, err := c.client().Do(req)
		if err != nil {
			c.report("刷新请求失败: " + err.Error())
			return
		}
		defer resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusNoContent, resp.StatusCode == http.StatusOK:
		case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
			c.report(fmt.Sprintf("刷新被拒（%d）：检查 API Key 权限", resp.StatusCode))
		default:
			c.report(fmt.Sprintf("刷新异常状态 %d", resp.StatusCode))
		}
	}()
}

func (c *Client) auth(req *http.Request) {
	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=%q", c.APIKey))
}

func (c *Client) report(msg string) {
	if c.OnError != nil {
		c.OnError(msg)
	}
}
