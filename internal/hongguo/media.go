package hongguo

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/2017fighting/guo/internal/pipeline"
)

const playbackAPI = "https://djapi.999888456.xyz/api/hongguo/play"
const webBaseURL = "https://hongguoduanju.com"

var numericID = regexp.MustCompile(`^[0-9]{1,32}$`)
var qualityNumber = regexp.MustCompile(`[0-9]+`)

// ResolveStream 实现 pipeline.Source：App API 取流为主，失败回落第三方兜底 API。
// TODO(下一块): Web 播放器页 SSR 线路（h264 无加密、guoapp 首选）尚未移植。
func (c *Client) ResolveStream(ctx context.Context, seriesID, vid string, quality int) (*pipeline.Stream, error) {
	if !numericID.MatchString(seriesID) || !numericID.MatchString(vid) {
		return nil, errors.New("红果视频 ID 无效")
	}
	stream, appErr := c.resolveAppStream(ctx, vid)
	if appErr == nil {
		return stream, nil
	}
	stream, fbErr := c.resolveFallbackStream(ctx, seriesID, vid)
	if fbErr == nil {
		return stream, nil
	}
	return nil, fmt.Errorf("App 线路: %v；兜底线路: %v", appErr, fbErr)
}

// ---- App API 取流（video_model） ----

func (c *Client) resolveAppStream(ctx context.Context, vid string) (*pipeline.Stream, error) {
	payload := map[string]any{
		"video_id": vid, "content_type": 1,
		"biz_param": map[string]any{"need_all_video_definition": true, "video_platform": 3},
	}
	result, err := c.appRequest(ctx, http.MethodPost, "/novel/player/video_model/v1/", nil, payload, false)
	if err != nil {
		return nil, err
	}
	data := nestedMap(result, "data")
	model, _ := data["video_model"].(map[string]any)
	if encoded, ok := data["video_model"].(string); ok {
		decoder := json.NewDecoder(strings.NewReader(encoded))
		decoder.UseNumber()
		if err := decoder.Decode(&model); err != nil {
			return nil, errors.New("红果 App 播放信息格式异常")
		}
	}
	return selectAppStream(model, vid)
}

type scoredStream struct {
	stream pipeline.Stream
	score  int
}

// selectAppStream 按画质打分选最优档：height*10，h264/avc1 +1，
// bytevc1/2 降权 -100000（不硬 ban，留作最后尝试——v11 策略）。
func selectAppStream(model map[string]any, vid string) (*pipeline.Stream, error) {
	variants := anyList(model["video_list"])
	if rows, ok := model["video_list"].(map[string]any); ok && len(variants) == 0 {
		keys := make([]string, 0, len(rows))
		for key := range rows {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			variants = append(variants, rows[key])
		}
	}
	durationSec, _ := strconv.ParseFloat(mapString(model, "video_duration", "duration"), 64)
	var keyErr error
	var choices []scoredStream
	for _, row := range variants {
		variant, _ := row.(map[string]any)
		meta := nestedMap(variant, "video_meta")
		codec := strings.ToLower(mapString(meta, "codec_type"))
		isBytevc := codec == "bytevc2" || codec == "bytevc1"
		addresses := mediaAddresses(variant)
		if len(addresses) == 0 {
			continue
		}
		stream := pipeline.Stream{
			Referer:    "", // App/兜底线路 CDN 不带 Referer（v9 实测：带了反而 403）
			DurationMS: int64(durationSec * 1000),
		}
		encryption := nestedMap(variant, "encrypt_info")
		spade := mapString(encryption, "spade_a")
		if spade != "" || encryption["encrypt"] == true || mapString(encryption, "encryption_method") == "cenc-aes-ctr" {
			key, err := contentKey(spade)
			if err != nil {
				keyErr = err
				continue
			}
			stream.CENCKeyHex = hex.EncodeToString(key)
		}
		height, _ := strconv.Atoi(mapString(meta, "vheight"))
		if definition, err := strconv.Atoi(qualityNumber.FindString(mapString(meta, "definition"))); err == nil && definition > 0 {
			height = definition
		} else if width, _ := strconv.Atoi(mapString(meta, "vwidth")); width > 0 && (height == 0 || width < height) {
			height = width
		}
		stream.Quality = height
		stream.Definition = mapString(meta, "definition")
		score := height * 10
		if codec == "h264" || codec == "avc1" {
			score++
		}
		if isBytevc {
			stream.Quality = 0
			score -= 100000
		}
		for _, address := range addresses {
			s := stream
			s.URL = address
			choices = append(choices, scoredStream{stream: s, score: score})
		}
	}
	if len(choices) > 0 {
		sort.SliceStable(choices, func(i, j int) bool { return choices[i].score > choices[j].score })
		best := choices[0].stream
		return &best, nil
	}
	if keyErr != nil {
		return nil, fmt.Errorf("红果 App 媒体密钥不可用: %w", keyErr)
	}
	return nil, errors.New("红果 App 未返回可用媒体档位")
}

// mediaAddresses 提取地址族（明文 URL 或 base64 编码 URL），按键序尝试。
func mediaAddresses(info map[string]any) []string {
	var addresses []string
	seen := map[string]bool{}
	var add func(any)
	add = func(value any) {
		switch value := value.(type) {
		case string:
			address := strings.TrimSpace(value)
			if len(address) > 8192 {
				return
			}
			if !isHTTPURL(address) {
				decoded, err := decodeBase64(address)
				if err != nil {
					return
				}
				address = strings.TrimSpace(string(decoded))
			}
			if isHTTPURL(address) && !seen[address] {
				seen[address] = true
				addresses = append(addresses, address)
			}
		case []any:
			for _, item := range value {
				add(item)
			}
		}
	}
	for _, key := range []string{"main_url", "backup_url", "backup_url_1", "backup_url_2", "backup_urls", "url_list"} {
		add(info[key])
	}
	return addresses
}

func isHTTPURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// ---- 第三方兜底取流 API ----

type playbackReference struct {
	ContentType   int    `json:"content_type"`
	FromVideoID   string `json:"from_video_id"`
	SeriesID      string `json:"series_id"`
	VideoID       string `json:"vid"`
	VideoPlatform int    `json:"video_platform"`
}

type playbackResponse struct {
	Parse   json.RawMessage `json:"parse"`
	JX      json.RawMessage `json:"jx"`
	KeyURLs []struct {
		Name   string `json:"name"`
		URL    string `json:"src"`
		KeyID  string `json:"kid"`
		SpadeA string `json:"spade_a"`
	} `json:"key_urls"`
}

func (c *Client) resolveFallbackStream(ctx context.Context, seriesID, vid string) (*pipeline.Stream, error) {
	reference, err := json.Marshal(playbackReference{ContentType: 1004, SeriesID: seriesID, VideoID: vid, VideoPlatform: 3})
	if err != nil {
		return nil, err
	}
	query := url.Values{"id": {base64.StdEncoding.EncodeToString(reference)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, playbackAPI+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1")
	req.Header.Set("Referer", webBaseURL+"/")
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	decoded, err := decodePlaybackResponse(string(body))
	if err != nil {
		return nil, err
	}
	var response playbackResponse
	if err := json.Unmarshal(decoded, &response); err != nil {
		return nil, errors.New("红果兜底播放接口返回了无效数据")
	}
	for _, flag := range []json.RawMessage{response.Parse, response.JX} {
		switch strings.TrimSpace(string(flag)) {
		case "", "null", "false", "0", `"0"`, `""`:
		default:
			return nil, errors.New("红果兜底播放接口没有返回直接媒体地址")
		}
	}
	var best *pipeline.Stream
	bestQuality := -1
	var keyErr error
	for _, option := range response.KeyURLs {
		mediaURL := strings.TrimSpace(option.URL)
		if len(mediaURL) > 8192 || !isHTTPURL(mediaURL) {
			continue
		}
		keyID, err := hex.DecodeString(strings.TrimSpace(option.KeyID))
		if err != nil || len(keyID) != aes.BlockSize {
			keyErr = errors.New("红果媒体密钥标识无效")
			continue
		}
		key, err := contentKey(option.SpadeA)
		if err != nil {
			keyErr = err
			continue
		}
		quality, _ := strconv.Atoi(qualityNumber.FindString(option.Name))
		if best == nil || quality > bestQuality {
			best = &pipeline.Stream{
				URL: mediaURL, Referer: "", CENCKeyHex: hex.EncodeToString(key),
				Quality: quality, Definition: qualityNumber.FindString(option.Name),
			}
			bestQuality = quality
		}
	}
	if best != nil {
		return best, nil
	}
	if keyErr != nil {
		return nil, keyErr
	}
	return nil, errors.New("红果兜底播放接口未返回该集可用的媒体和密钥")
}

// decodePlaybackResponse 解开 `v2.<hex>.<base64>` 加密信封（AES-128-CBC + PKCS7）。
func decodePlaybackResponse(body string) ([]byte, error) {
	text := strings.TrimSpace(body)
	if !strings.HasPrefix(text, "v2.") {
		return []byte(text), nil
	}
	parts := strings.SplitN(text, ".", 3)
	if len(parts) != 3 || len(parts[1]) <= 4 || len(parts[1]) > 1028 {
		return nil, errors.New("红果兜底接口响应密钥无效")
	}
	encoded, err := hex.DecodeString(parts[1][4:])
	if err != nil || len(encoded) < 32 {
		return nil, errors.New("红果兜底接口响应密钥无效")
	}
	mask := [...]byte{104, 64, 70, 166, 190, 168, 143, 130, 225, 254, 251, 217, 196, 34, 45, 60, 29, 20, 103, 105}
	material := make([]byte, len(encoded))
	for index, current := range encoded {
		previous := byte(109)
		if index > 0 {
			previous = encoded[index-1]
		}
		slot := index % len(mask)
		salt := mask[slot] ^ byte(90+13*slot) ^ 85
		shifted := byte(int(current) + 215 - 11*index)
		material[index] = previous ^ salt ^ bits.RotateLeft8(shifted, 3)
	}
	ciphertext, err := decodeBase64(parts[2])
	if err != nil || len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, errors.New("红果兜底接口加密响应无效")
	}
	block, err := aes.NewCipher(material[:16])
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, material[16:32]).CryptBlocks(plain, ciphertext)
	return pkcs7Unpad(plain, aes.BlockSize)
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, errors.New("invalid padding")
	}
	padding := int(data[len(data)-1])
	if padding == 0 || padding > blockSize || padding > len(data) {
		return nil, errors.New("invalid padding")
	}
	for _, b := range data[len(data)-padding:] {
		if int(b) != padding {
			return nil, errors.New("invalid padding")
		}
	}
	return data[:len(data)-padding], nil
}

func decodeBase64(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if decoded, err := base64.StdEncoding.Strict().DecodeString(value); err == nil {
		return decoded, nil
	}
	return base64.RawStdEncoding.Strict().DecodeString(value)
}

// contentKey 从 spade_a 混淆串提取 AES-128 CENC 内容密钥（协议文档 §6.2）。
func contentKey(value string) ([]byte, error) {
	if len(value) > 1024 {
		return nil, errors.New("红果媒体密钥数据过长")
	}
	raw, err := decodeBase64(value)
	if err != nil || len(raw) < 3 {
		return nil, errors.New("红果媒体密钥编码无效")
	}
	tagLength := int(raw[0]^raw[1]^raw[2]) - 48
	contentLength := len(raw) - tagLength - 1
	if tagLength < 1 || contentLength < 33 || contentLength >= len(raw) {
		return nil, errors.New("红果媒体密钥结构无效")
	}
	seed := raw[len(raw)-tagLength-2] ^ raw[len(raw)-tagLength-1]
	tag := make([]byte, tagLength)
	for index := range tag {
		tag[index] = raw[len(raw)-tagLength+index] ^ seed
	}
	if string(tag) == "app_v2" || string(tag) == "web_v2" {
		return nil, errors.New("红果媒体密钥版本暂不支持")
	}
	decoded := make([]byte, contentLength)
	previousEven, previousOdd := byte(250), byte(85)
	for index, current := range raw[1 : 1+contentLength] {
		previous := previousEven
		if index%2 == 0 {
			previousEven = current
		} else {
			previous = previousOdd
			previousOdd = current
		}
		decoded[index] = byte(int(previous^current) - 21 - bits.OnesCount(uint(index)))
	}
	padding, err := strconv.ParseUint(string(decoded[:1]), 36, 8)
	if err != nil || contentLength-int(padding)-1 != 32 {
		return nil, errors.New("红果媒体密钥内容无效")
	}
	key, err := hex.DecodeString(string(decoded[1:33]))
	if err != nil || len(key) != aes.BlockSize {
		return nil, errors.New("红果媒体密钥不是有效的 AES-128 密钥")
	}
	return key, nil
}
