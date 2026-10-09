package nfo

import (
	"encoding/xml"
	"strings"
	"testing"
)

func decode(t *testing.T, b []byte) string {
	t.Helper()
	s := string(b)
	if !strings.HasPrefix(s, xml.Header) {
		t.Fatalf("missing xml header: %q", s[:40])
	}
	return strings.TrimPrefix(s, xml.Header)
}

func TestShowNFO(t *testing.T) {
	doc := decode(t, mustMarshal(t, Show{
		Title: "闪婚老伴是豪门", Plot: "讲述闪婚的故事<b>含特殊字符 & 引号\"'</b>",
		Year: "2025", Genres: []string{"都市", "甜宠"}, UniqueID: "7345678",
	}))
	for _, want := range []string{
		"<title>闪婚老伴是豪门</title>",
		"<year>2025</year>",
		"<genre>都市</genre>",
		"<studio>", // 空省略——studio 留空时应消失
	} {
		if want == "<studio>" && strings.Contains(doc, want) {
			t.Errorf("empty studio should be omitted")
		}
	}
	if !strings.Contains(doc, `<uniqueid type="hongguo" default="true">7345678</uniqueid>`) {
		t.Errorf("uniqueid wrong:\n%s", doc)
	}
	if !strings.Contains(doc, "<lockdata>true</lockdata>") {
		t.Errorf("lockdata missing")
	}
	if strings.Contains(doc, "<thumb") {
		t.Errorf("thumb must not appear in tvshow.nfo")
	}
	if !strings.Contains(doc, "含特殊字符 &amp; 引号&#34;&#39;&lt;/b&gt;") {
		t.Errorf("escaping wrong:\n%s", doc)
	}
}

func TestEpisodeNFO(t *testing.T) {
	doc := decode(t, mustMarshalEp(t, Episode{
		Title: "第 3 集", ShowTitle: "闪婚老伴是豪门 (2025)", Season: 1, Episode: 3,
		Plot: "分集简介", UniqueID: "800123",
	}))
	for _, want := range []string{
		"<title>第 3 集</title>",
		"<showtitle>闪婚老伴是豪门 (2025)</showtitle>",
		"<season>1</season>",
		"<episode>3</episode>",
		`<uniqueid type="hongguo" default="true">800123</uniqueid>`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("missing %q in:\n%s", want, doc)
		}
	}
}

func TestClean(_ *testing.T) {}

func mustMarshal(t *testing.T, s Show) []byte {
	t.Helper()
	b, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustMarshalEp(t *testing.T, e Episode) []byte {
	t.Helper()
	b, err := e.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return b
}
