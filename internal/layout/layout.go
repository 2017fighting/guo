// Package layout 计算媒体库内的目录与文件路径，遵循 Jellyfin 剧集库布局
// （docs/research/jellyfin-integration.md 第 1 节）：
//
//	MEDIA_DIR/
//	└── 剧名 (年份)/
//	    ├── tvshow.nfo / poster.jpg / fanart.jpg
//	    └── Season 01/S01E001.{mp4,nfo,zh.ass}（+ S01E001-thumb.jpg 可选）
package layout

import (
	"fmt"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SafeName 清理剧目录名：剔除文件系统保留字符与控制字符并截断，
// 对齐 Jellyfin 官方保留字符清单（< > : " / \ | ? *）。
func SafeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || (r >= 0x7f && r < 0xa0) {
			continue
		}
		switch r {
		case '<', '>', ':', '"', '/', '\\', '|', '?', '*':
			continue
		}
		b.WriteRune(r)
	}
	s := strings.TrimSpace(b.String())
	if s == "" {
		s = "未命名"
	}
	if utf8.RuneCountInString(s) > 60 {
		s = string([]rune(s)[:60])
	}
	return s
}

// ShowDir 返回剧目录：MEDIA_DIR/剧名 (年份)。年份为空时只有剧名。
func ShowDir(root, title, year string) string {
	name := SafeName(title)
	if year != "" {
		name = fmt.Sprintf("%s (%s)", name, SafeName(year))
	}
	return path.Join(root, name)
}

// SeasonDir 返回季目录：剧目录/Season NN（季号补零到两位，短剧恒为 1）。
func SeasonDir(showDir string, season int) string {
	return path.Join(showDir, fmt.Sprintf("Season %02d", season))
}

// EpisodeStem 返回分集文件基名：SxxEyyy（季两位、集三位，集数上限 999）。
func EpisodeStem(season, episode int) string {
	if episode > 999 {
		episode = 999
	}
	return fmt.Sprintf("S%02dE%03d", season, episode)
}

// Show 集中剧级产物路径。
type Show struct{ Dir string }

func (s Show) NFO() string    { return path.Join(s.Dir, "tvshow.nfo") }
func (s Show) Poster() string { return path.Join(s.Dir, "poster.jpg") }
func (s Show) Fanart() string { return path.Join(s.Dir, "fanart.jpg") }

// Episode 集中分集产物路径（与视频同基名同目录）。
type Episode struct{ Dir, Stem string }

func (e Episode) Video() string { return path.Join(e.Dir, e.Stem+".mp4") }
func (e Episode) NFO() string   { return path.Join(e.Dir, e.Stem+".nfo") }
func (e Episode) ASS() string   { return path.Join(e.Dir, e.Stem+".zh.ass") }
func (e Episode) Thumb() string { return path.Join(e.Dir, e.Stem+"-thumb.jpg") }
func (e Episode) Part() string  { return path.Join(e.Dir, e.Stem+".part") }

var _ = unicode.IsSpace // 保留：将来按需扩展
