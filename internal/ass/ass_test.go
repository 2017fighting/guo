package ass

import (
	"bytes"
	"strings"
	"testing"
)

func decodeLines(t *testing.T, b []byte) []string {
	t.Helper()
	s := string(bytes.TrimPrefix(b, []byte("\xEF\xBB\xBF")))
	lines := strings.Split(s, "\r\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for _, l := range lines {
		if strings.Contains(l, "\n") { // 不允许残留 LF
			t.Fatalf("found bare LF in %q", l)
		}
	}
	return lines
}

func TestHeadShape(t *testing.T) {
	lines := decodeLines(t, Convert(nil, Options{}))
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"PlayResX: 1920", "PlayResY: 1080", "Collisions: Normal",
		"WrapStyle: 2", "ScaledBorderAndShadow: yes", "YCbCr Matrix: TV.601",
		"Style: Danmaku, 黑体, 30.0, &H00FFFFFF, &H00FFFFFF, &H00000000, &H00000000, 0, 0, 0, 0, 100, 100, 0.00, 0.00, 1, 1, 0, 7, 0, 0, 0, 0",
		"Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("head missing %q", want)
		}
	}
}

func TestTimestampRounding(t *testing.T) {
	cases := []struct {
		sec  float64
		want string
	}{
		{0, "0:00:00.00"},
		{5, "0:00:05.00"},
		{83.454, "0:01:23.45"},
		{83.455, "0:01:23.46"}, // 四舍五入而非截断
		{3672.5, "1:01:12.50"},
	}
	for _, c := range cases {
		if got := timestamp(c.sec); got != c.want {
			t.Errorf("timestamp(%v) = %q, want %q", c.sec, got, c.want)
		}
	}
}

func TestEscape(t *testing.T) {
	cases := []struct{ in, want string }{
		{`a\b`, `a\​b`},
		{"{tag}", `\{tag\}`},
		{"a\nb", `a\Nb`},
		{" x ", "\u200b x \u200b"},
	}
	for _, c := range cases {
		if got := escape(c.in); got != c.want {
			t.Errorf("escape(%q) = %q (%v), want %q", c.in, got, []rune(got), c.want)
		}
	}
}

func TestSanitize(t *testing.T) {
	if got := sanitize("a\x01b\u2028c  "); got != "a b c" {
		t.Errorf("got %q", got)
	}
	long := strings.Repeat("字", 200)
	if got := sanitize(long); len([]rune(got)) != 181 || !strings.HasSuffix(got, "…") {
		t.Errorf("long text not truncated: %d runes", len([]rune(got)))
	}
}

func TestDialogueEventShape(t *testing.T) {
	out := Convert([]Comment{{ID: "1", Text: "前方高能", TimeMS: 15000}}, Options{})
	if !bytes.Contains(out, []byte("Dialogue: 2,0:00:15.00,0:00:23.00,Danmaku,,0000,0000,0000,")) {
		t.Fatalf("dialogue prefix wrong:\n%s", out)
	}
	// 4 字 × 字号 30 = 宽 120：\move(1920,row,-120,row)
	if !bytes.Contains(out, []byte(`{\move(1920,0,-120,0)}前方高能`)) {
		t.Fatalf("move tag wrong:\n%s", out)
	}
}

func TestRowsStackDownward(t *testing.T) {
	// 同一时刻多条弹幕应自上而下铺行：0、30、60……
	cs := make([]Comment, 0, 5)
	for i := 0; i < 5; i++ {
		cs = append(cs, Comment{ID: string(rune('a' + i)), Text: "弹幕", TimeMS: 1000})
	}
	out := Convert(cs, Options{})
	for i := 0; i < 5; i++ {
		row := i * 30
		tag := `{\move(1920,` + itoa(row) + `,-60,` + itoa(row) + `)}`
		if !bytes.Contains(out, []byte(tag)) {
			t.Errorf("comment %d not on row %d", i, row)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestDedupSortAndClean(t *testing.T) {
	cs := []Comment{
		{ID: "x", Text: "晚到", TimeMS: 9000},
		{ID: "a", Text: "早到", TimeMS: 1000},
		{ID: "a", Text: "重复", TimeMS: 2000},   // 同 ID 去重，保留先出现
		{ID: "b", Text: "\x01", TimeMS: 3000}, // 净化后为空，整条丢弃
	}
	out := Convert(cs, Options{})
	s := string(out)
	if !strings.Contains(s, "早到") || !strings.Contains(s, "晚到") || strings.Contains(s, "重复") {
		t.Errorf("dedup/sort wrong")
	}
}

func TestScreenFullOverlapsNotDrops(t *testing.T) {
	// 1080 高 / 30 字号 = 36 行；同一时刻塞 100 条，全部应有 Dialogue（重叠不丢）。
	cs := make([]Comment, 100)
	for i := range cs {
		cs[i] = Comment{ID: string(rune('A'+i%26)) + itoa(i), Text: "满", TimeMS: 500}
	}
	out := Convert(cs, Options{})
	if n := strings.Count(string(out), "Dialogue: 2,"); n != 100 {
		t.Errorf("expected 100 dialogues, got %d", n)
	}
}
