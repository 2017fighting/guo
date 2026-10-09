package layout

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"流星追击", "流星追击"},
		{"a<b>c:d\"e/f\\g|h?i*j", "abcdefghij"},
		{"  空白  ", "空白"},
		{"", "未命名"},
		{"控制\x01\x1f字符", "控制字符"},
		{strings.Repeat("长", 80), strings.Repeat("长", 60)},
	}
	for _, c := range cases {
		if got := SafeName(c.in); got != c.want {
			t.Errorf("SafeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestShowDir(t *testing.T) {
	if got := ShowDir("/media", "闪婚老伴", "2025"); got != "/media/闪婚老伴 (2025)" {
		t.Errorf("got %q", got)
	}
	if got := ShowDir("/media", "闪婚老伴", ""); got != "/media/闪婚老伴" {
		t.Errorf("no-year got %q", got)
	}
}

func TestSeasonDirAndStem(t *testing.T) {
	if got := SeasonDir("/m/s", 1); !strings.HasSuffix(filepath.ToSlash(got), "/Season 01") {
		t.Errorf("got %q", got)
	}
	if got := EpisodeStem(1, 1); got != "S01E001" {
		t.Errorf("got %q", got)
	}
	if got := EpisodeStem(1, 1200); got != "S01E999" {
		t.Errorf("clamp got %q", got)
	}
}

func TestEpisodePaths(t *testing.T) {
	e := Episode{Dir: "/m/s/Season 01", Stem: "S01E003"}
	want := map[string]string{
		"Video": "/m/s/Season 01/S01E003.mp4",
		"NFO":   "/m/s/Season 01/S01E003.nfo",
		"ASS":   "/m/s/Season 01/S01E003.zh.ass",
		"Thumb": "/m/s/Season 01/S01E003-thumb.jpg",
		"Part":  "/m/s/Season 01/S01E003.part",
	}
	if e.Video() != want["Video"] || e.NFO() != want["NFO"] || e.ASS() != want["ASS"] ||
		e.Thumb() != want["Thumb"] || e.Part() != want["Part"] {
		t.Errorf("paths = %q %q %q %q %q", e.Video(), e.NFO(), e.ASS(), e.Thumb(), e.Part())
	}
}
