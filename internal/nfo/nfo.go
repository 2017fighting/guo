// Package nfo 生成 Jellyfin（Emby 兼容）NFO 文件。
//
// 字段依据 docs/research/jellyfin-integration.md 第 2 节：
//   - tvshow.nfo：title/plot/uniqueid(+year/premiered/genre/studio)，lockdata 锁本地元数据；
//     不写 <thumb>（相对路径不被解析；图片全部走文件名规则）
//   - 分集 NFO：title/showtitle/season/episode/plot/uniqueid（与视频同基名同目录）
package nfo

import (
	"encoding/xml"
	"strings"
)

// Show 剧级元数据（来源：红果详情卡片）。
type Show struct {
	Title    string
	Plot     string
	Year     string // 可选，>1850 才生效
	Premiered string // 可选，如 2025-03-01
	Genres   []string
	Studio   string
	UniqueID string // 红果 seriesID
}

// Episode 分集元数据。
type Episode struct {
	Title    string // 如「第 3 集」
	ShowTitle string // 与剧目录名一致
	Season   int
	Episode  int
	Plot     string
	UniqueID string // 红果 videoID
}

type uniqueID struct {
	XMLName xml.Name `xml:"uniqueid"`
	Type    string   `xml:"type,attr"`
	Default string   `xml:"default,attr"`
	Value   string   `xml:",chardata"`
}

type xmlShow struct {
	XMLName    xml.Name `xml:"tvshow"`
	Title      string   `xml:"title"`
	Plot       string   `xml:"plot"`
	Year       string   `xml:"year,omitempty"`
	Premiered  string   `xml:"premiered,omitempty"`
	Genres     []string `xml:"genre"`
	Studio     string   `xml:"studio,omitempty"`
	UniqueID   uniqueID
	LockData   bool     `xml:"lockdata"`
}

type xmlEpisode struct {
	XMLName   xml.Name `xml:"episodedetails"`
	Title     string   `xml:"title"`
	ShowTitle string   `xml:"showtitle"`
	Season    int      `xml:"season"`
	Episode   int      `xml:"episode"`
	Plot      string   `xml:"plot"`
	UniqueID  uniqueID
}

// Show 返回 tvshow.nfo 内容（UTF-8，xml 声明头）。
func (s Show) Marshal() ([]byte, error) {
	v := xmlShow{
		Title: s.Title, Plot: s.Plot, Year: s.Year, Premiered: s.Premiered,
		Genres: s.Genres, Studio: s.Studio,
		UniqueID: uniqueID{Type: "hongguo", Default: "true", Value: s.UniqueID},
		LockData: true,
	}
	out, err := xml.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return []byte(xml.Header + string(out) + "\n"), nil
}

// Episode 返回分集 .nfo 内容。
func (e Episode) Marshal() ([]byte, error) {
	v := xmlEpisode{
		Title: e.Title, ShowTitle: e.ShowTitle, Season: e.Season, Episode: e.Episode,
		Plot: e.Plot, UniqueID: uniqueID{Type: "hongguo", Default: "true", Value: e.UniqueID},
	}
	out, err := xml.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return []byte(xml.Header + string(out) + "\n"), nil
}

// Clean 剔除 XML 非法控制字符（正文已在源侧净化，这里兜底）。
func Clean(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
