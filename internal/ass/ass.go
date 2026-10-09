// Package ass 将红果弹幕（仅 ID/文本/毫秒偏移三字段）转换为 ASS 外挂字幕。
//
// 依据 ADR-0001：本实现为清洁重写，不使用 danmaku2ass 代码；
// 行为语义依据 docs/research/danmaku-ass.md 的公开格式要点：
//   - 头部：PlayRes 1920x1080、Collisions Normal、WrapStyle 2、
//     ScaledBorderAndShadow yes、YCbCr Matrix TV.601
//   - 事件：全部映射为右→左滚动（\move），时长默认 8s，
//     每条弹幕用满时长走完 (W+w) 距离（速度模型 (W+w)/duration px/s）
//   - 布局：逐像素行占用表；新弹幕入屏与旧弹幕离屏两判定（追尾模型）
//   - 文件：UTF-8 BOM + CRLF；时间戳四舍五入到厘秒
package ass

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

// Options 控制转换参数。
type Options struct {
	PlayResX, PlayResY int    // 舞台尺寸，默认 1920x1080
	FontSize           int    // 字号，默认 30
	FontFace           string // 字体名，默认 黑体
	DurationSec        float64
}

func (o *Options) fill() {
	if o.PlayResX <= 0 {
		o.PlayResX = 1920
	}
	if o.PlayResY <= 0 {
		o.PlayResY = 1080
	}
	if o.FontSize <= 0 {
		o.FontSize = 30
	}
	if o.FontFace == "" {
		o.FontFace = "黑体"
	}
	if o.DurationSec <= 0 {
		o.DurationSec = 8
	}
}

// Comment 一条弹幕：文本 + 距片头毫秒偏移。
type Comment struct {
	ID     string
	Text   string
	TimeMS int64
}

// Convert 把整集弹幕转为 ASS 字节流。输入无需预排序（内部按时间稳定排序、按 ID 去重）。
func Convert(comments []Comment, opts Options) []byte {
	opts.fill()

	// 净化 + 去重 + 时间稳定排序（对齐 guoapp parseHongguoDanmaku 口径）。
	seen := make(map[string]struct{}, len(comments))
	clean := make([]Comment, 0, len(comments))
	for _, c := range comments {
		c.Text = sanitize(c.Text)
		if c.Text == "" {
			continue
		}
		if c.ID != "" {
			if _, dup := seen[c.ID]; dup {
				continue
			}
			seen[c.ID] = struct{}{}
		}
		clean = append(clean, c)
	}
	sort.SliceStable(clean, func(i, j int) bool { return clean[i].TimeMS < clean[j].TimeMS })

	var buf bytes.Buffer
	buf.WriteString("\xEF\xBB\xBF") // UTF-8 BOM
	writeHead(&buf, opts)
	layoutEvents(&buf, clean, opts)
	return buf.Bytes()
}

func writeHead(buf *bytes.Buffer, o Options) {
	outline := int(math.Max(float64(o.FontSize)/25.0, 1))
	fprintf := func(f string, a ...any) { fmt.Fprintf(buf, f, a...) }
	fprintf("[Script Info]\r\n")
	fprintf("ScriptType: v4.00+\r\n")
	fprintf("PlayResX: %d\r\n", o.PlayResX)
	fprintf("PlayResY: %d\r\n", o.PlayResY)
	fprintf("Aspect Ratio: %d:%d\r\n", o.PlayResX, o.PlayResY)
	fprintf("Collisions: Normal\r\n")
	fprintf("WrapStyle: 2\r\n")
	fprintf("ScaledBorderAndShadow: yes\r\n")
	fprintf("YCbCr Matrix: TV.601\r\n\r\n")
	fprintf("[V4+ Styles]\r\n")
	fprintf("Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\r\n")
	fprintf("Style: Danmaku, %s, %d.0, &H00FFFFFF, &H00FFFFFF, &H00000000, &H00000000, 0, 0, 0, 0, 100, 100, 0.00, 0.00, 1, %d, 0, 7, 0, 0, 0, 0\r\n\r\n",
		o.FontFace, o.FontSize, outline)
	fprintf("[Events]\r\n")
	fprintf("Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\r\n")
}

// event 是布局中间态。
type event struct {
	start  float64 // 秒
	text   string
	w, h   int // 像素宽高（单行：h = 字号）
	row    int
	valid  bool // false = 无可用行（屏幕满，重叠到最早行）
	assEnd float64
}

// layoutEvents 做行占用布局并写 Dialogue 行。
func layoutEvents(buf *bytes.Buffer, comments []Comment, o Options) {
	dm := o.DurationSec
	type slot struct {
		start float64
		w     int
	}
	rows := make([]slot, o.PlayResY)

	events := make([]event, len(comments))
	for i, c := range comments {
		start := float64(c.TimeMS) / 1000.0
		w := int(runeWidth(c.Text) * o.FontSize)
		events[i] = event{start: start, text: c.Text, w: w, h: o.FontSize, assEnd: start + dm}
	}

	for i := range events {
		ev := &events[i]
		row := 0
		placed := false
		for row+ev.h <= o.PlayResY {
			free := 0
			for r := row; r < row+ev.h; r++ {
				if rowFree(rows[r], ev, dm, o.PlayResX) {
					free++
				} else {
					break
				}
			}
			if free >= ev.h {
				for r := row; r < row+ev.h; r++ {
					rows[r] = slot{start: ev.start, w: ev.w}
				}
				ev.row = row
				ev.valid = true
				placed = true
				break
			}
			row += free
			if free == 0 {
				row++
			}
		}
		if !placed {
			// 屏幕满：重叠到最早入屏弹幕所在行（不丢数据）。
			minRow, minStart := 0, math.MaxFloat64
			for r, s := range rows {
				if s.start < minStart {
					minStart, minRow = s.start, r
				}
			}
			ev.row = minRow
			ev.valid = false
			for r := ev.row; r < ev.row+ev.h && r < o.PlayResY; r++ {
				rows[r] = slot{start: ev.start, w: ev.w}
			}
		}
	}

	for _, ev := range events {
		fmt.Fprintf(buf, "Dialogue: 2,%s,%s,Danmaku,,0000,0000,0000,,{\\move(%d,%d,%d,%d)}%s\r\n",
			timestamp(ev.start), timestamp(ev.assEnd),
			o.PlayResX, ev.row, -ev.w, ev.row, escape(ev.text))
	}
}

// rowFree 判定某像素行在该弹幕入屏时刻是否空闲（追尾速度模型，单位秒）。
// 占用判定两条（任一成立即占用）：
//   - 旧弹幕入屏太晚：old.start > new.start - dm*(1 - W/(w_new+W))
//   - 旧弹幕尾部未完全入屏：old.start + dm*w_old/(w_old+W) > new.start
func rowFree(old struct {
	start float64
	w     int
}, ev *event, dm float64, W int) bool {
	wn := float64(ev.w)
	wo := float64(old.w)
	if old.start > ev.start-dm*(1-float64(W)/(wn+float64(W))) {
		return false
	}
	if old.start+dm*wo/(wo+float64(W)) > ev.start {
		return false
	}
	return true
}

// timestamp 秒 → 厘秒四舍五入，格式 H:MM:SS.CC（小时不补零）。
func timestamp(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	cs := int(math.Round(sec * 100))
	h := cs / 360000
	cs %= 360000
	m := cs / 6000
	cs %= 6000
	s := cs / 100
	cs %= 100
	return fmt.Sprintf("%d:%02d:%02d.%02d", h, m, s, cs)
}

// escape 转义 ASS 覆盖标签：反斜杠、花括号、换行；首尾空白以零宽空格垫护。
func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString("\\\u200b")
		case '{':
			b.WriteString("\\{")
		case '}':
			b.WriteString("\\}")
		case '\n', '\r':
			b.WriteString("\\N")
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	if strings.TrimLeft(out, " \t") != out {
		out = "\u200b" + out
	}
	if strings.TrimRight(out, " \t") != out {
		out += "\u200b"
	}
	return out
}

// sanitize 净化弹幕文本：控制字符与行分隔符替换为空格、Trim、超 180 rune 截断加省略号。
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == 0x2028 || r == 0x2029 {
			b.WriteRune(' ')
		} else {
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if utf8.RuneCountInString(out) > 180 {
		out = string([]rune(out)[:180]) + "…"
	}
	return out
}

// runeWidth 以最长行的 rune 数估宽（CJK 场景够用；勿用字节长度）。
func runeWidth(s string) int {
	max := 0
	for _, line := range strings.Split(s, "\n") {
		if n := utf8.RuneCountInString(line); n > max {
			max = n
		}
	}
	return max
}
