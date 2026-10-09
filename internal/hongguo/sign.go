// Package hongguo 实现红果站源客户端（pipeline.Source），
// 按 docs/research/hongguo-protocol.md 从 guoapp 参考实现移植：
//   - 设备身份：随机 19 位 device_id/iid 进程内固定（§5.1）
//   - 普通签名：X-Gorgon/X-Khronos/X-SS-Req-Ticket(+X-SS-STUB)（§5.3）
//   - 弹幕签名：Comment-Source/Server-Channel + X-Argus/X-Ladon（§5.4）
//   - 取流：App API（video_model）+ 第三方兜底 API；CENC 密钥从 spade_a 提取（§4/§6.2）
//   - 弹幕：30 秒窗口游标分页（§7）
package hongguo

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/bits"
	"net/http"
	"strconv"
	"time"
)

// newDeviceID 生成 19 位十进制设备号（1e18 + rand % 8e18），进程内固定一对。
func newDeviceID() string {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return strconv.FormatUint(1_000_000_000_000_000_000+binary.BigEndian.Uint64(random[:])%8_000_000_000_000_000_000, 10)
}

// signRequest 普通请求轻量 Gorgon（0x8404）：只对 query+body+时间戳敏感。
func signRequest(request *http.Request, body []byte, now time.Time) {
	timestamp := uint32(now.Unix())
	queryHash := md5.Sum([]byte(request.URL.RawQuery))
	var payload [20]byte
	copy(payload[:4], queryHash[:4])
	if body != nil {
		bodyHash := md5.Sum(body)
		copy(payload[4:8], bodyHash[:4])
		request.Header.Set("X-SS-STUB", fmt.Sprintf("%X", bodyHash))
	}
	copy(payload[12:16], []byte{0, 6, 11, 28})
	binary.BigEndian.PutUint32(payload[16:], timestamp)
	key := [...]byte{0x44, 0xb9, 0xb9, 0xd9, 0xa4, 0xae, 0xf9, 0xfc, 0xa4, 0x93, 0xaa, 0x75, 0x7c, 0xa3, 0xc2, 0xc4, 0xa4, 0x96, 0x93, 0x8f}
	for index := range payload {
		payload[index] ^= key[index]
	}
	for index := range payload {
		mixed := bits.RotateLeft8(payload[index], 4) ^ payload[(index+1)%len(payload)]
		payload[index] = bits.Reverse8(mixed) ^ 0xff ^ byte(len(payload))
	}
	signature := append([]byte{0x84, 0x04, 0x40, 0x1c, 0, 0}, payload[:]...)
	request.Header.Set("X-Khronos", strconv.FormatUint(uint64(timestamp), 10))
	request.Header.Set("X-Gorgon", hex.EncodeToString(signature))
	request.Header.Set("X-SS-Req-Ticket", strconv.FormatInt(now.UnixMilli(), 10))
}
