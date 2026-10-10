package hongguo

// 播放页取流与弹幕窗口的公开面（server 播放 lane 用）：
//   - 线路偏好：自动三级回退（默认）或指定 官网/App/兜底 单线路
//   - 画质档枚举与选档（quality>0 时优先取该档，取不到回落最优分档）
//   - 弹幕单窗口（30s 游标，播放页边播边拉；过滤口径与 DanmakuAll 同源）
// ResolveStream（pipeline.Source）语义不变，本文件是其播放页扩展。

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/2017fighting/guo/internal/ass"
	"github.com/2017fighting/guo/internal/pipeline"
)

// 取流线路（播放页线路切换）。
const (
	LineAuto     = ""         // 三级回退：官网 → App → 兜底（与 ResolveStream 同序）
	LineWeb      = "web"      // 官网播放器页（h264 明文，浏览器可直接播）
	LineApp      = "app"      // App API（多画质档，可能 CENC 加密）
	LineFallback = "fallback" // 第三方兜底 API（多画质档，可能 CENC 加密）
)

// QualityOption 画质档（播放页画质下拉）。
type QualityOption struct {
	ID    int    `json:"id"`    // 选档值（= stream 接口 quality 参数；0 = 自动/单一档）
	Label string `json:"label"` // 展示名（如 1080p）
}

// PlayStream 播放取流结果：生效流 + 线路 + 该线路可用画质档 + 画面方向。
type PlayStream struct {
	Stream    *pipeline.Stream // URL/Referer/CENCKeyHex/时长（Referer 按线路策略已带好）
	Line      string           // 生效线路 web|app|fallback
	Qualities []QualityOption  // 该线路可选画质档（空 = 单一档，只能自动）
	Vertical  bool             // 画面方向：线路可判定时按视频原始尺寸，未知按短剧默认竖屏
}

// ResolvePlayStream 按线路偏好 + 画质档解析播放流。
// line 为 LineAuto 时三级回退（官网 → App → 兜底），指定线路失败即报错不回落；
// quality>0 时在生效线路内优先选该档（App/兜底多档线路），官网线路忽略 quality。
func (c *Client) ResolvePlayStream(ctx context.Context, seriesID, vid, line string, quality int) (*PlayStream, error) {
	if !numericID.MatchString(seriesID) || !numericID.MatchString(vid) {
		return nil, errors.New("红果视频 ID 无效")
	}
	switch line {
	case LineAuto, LineWeb, LineApp, LineFallback:
	default:
		return nil, fmt.Errorf("未知取流线路 %q", line)
	}
	if line != LineAuto {
		return c.playLine(ctx, seriesID, vid, line, quality)
	}
	var webErr, appErr error
	if play, err := c.playLine(ctx, seriesID, vid, LineWeb, 0); err == nil {
		return play, nil
	} else {
		webErr = err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if play, err := c.playLine(ctx, seriesID, vid, LineApp, quality); err == nil {
		return play, nil
	} else {
		appErr = err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	play, err := c.playLine(ctx, seriesID, vid, LineFallback, quality)
	if err == nil {
		return play, nil
	}
	return nil, fmt.Errorf("网页取流: %v；App 取流: %v；兜底取流: %w", webErr, appErr, err)
}

func (c *Client) playLine(ctx context.Context, seriesID, vid, line string, quality int) (*PlayStream, error) {
	switch line {
	case LineWeb:
		stream, err := c.webStream(ctx, seriesID, vid)
		if err != nil {
			return nil, err
		}
		// 官网线路单档（明文 h264）：画质档为空（由服务端补「自动」项）；
		// 页面不带尺寸信息，按短剧默认竖屏，前端 loadedmetadata 后自适应校正。
		return &PlayStream{Stream: stream, Line: LineWeb, Qualities: nil, Vertical: true}, nil
	case LineApp:
		model, err := c.appModel(ctx, vid)
		if err != nil {
			return nil, err
		}
		choices, keyErr := appStreamChoices(model, vid)
		pick := pickAppChoice(choices, quality)
		if pick == nil {
			if keyErr != nil {
				return nil, fmt.Errorf("红果 App 媒体密钥不可用: %w", keyErr)
			}
			return nil, errors.New("红果 App 未返回可用媒体档位")
		}
		fresh := pick.stream
		return &PlayStream{
			Stream: &fresh, Line: LineApp,
			Qualities: appQualityOptions(choices), Vertical: pick.portrait(),
		}, nil
	default: // LineFallback
		candidates, err := c.fallbackCandidates(ctx, seriesID, vid)
		if err != nil {
			return nil, err
		}
		pick := pickFallbackStream(candidates, quality)
		fresh := *pick
		var opts []QualityOption
		seen := map[int]bool{}
		for _, cand := range candidates {
			if cand.Quality > 0 && !seen[cand.Quality] {
				seen[cand.Quality] = true
				opts = append(opts, QualityOption{ID: cand.Quality, Label: cand.Definition})
			}
		}
		// 兜底线路无尺寸信息，按短剧默认竖屏
		return &PlayStream{Stream: &fresh, Line: LineFallback, Qualities: opts, Vertical: true}, nil
	}
}

// appQualityOptions 从候选去重出画质档（按高度降序）。
func appQualityOptions(choices []scoredStream) []QualityOption {
	var opts []QualityOption
	seen := map[int]bool{}
	for _, choice := range choices { // 已按分数降序，同档位高分（可播）在前
		if choice.height <= 0 || seen[choice.height] {
			continue
		}
		seen[choice.height] = true
		label := choice.stream.Definition
		if label == "" {
			label = strconv.Itoa(choice.height) + "p"
		}
		opts = append(opts, QualityOption{ID: choice.height, Label: label})
	}
	return opts
}

// DanmakuWindow 拉取单个弹幕窗口（30s 游标语义；播放页边播边拉）。
// 返回窗口内弹幕（≤90 条，过滤口径与 DanmakuAll 同源）与下一窗口起点 nextMS。
func (c *Client) DanmakuWindow(ctx context.Context, seriesID, vid string, startMS, durationMS int64) ([]ass.Comment, int64, error) {
	page, err := c.danmakuWindow(ctx, seriesID, vid, startMS, durationMS)
	if err != nil {
		return nil, 0, err
	}
	return page.items, page.nextMS, nil
}
