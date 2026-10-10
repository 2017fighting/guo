package server

// SSE /api/v1/events：队列事件流。事件类型 v1 只有 "queue"，data 为
// 全量队列快照 JSON（与 GET /api/v1/downloads 同源同构，前端一个处理函数
// 即可复用）。连接即推首帧；之后每次 Signal（状态跃迁/进度心跳）推一帧；
// 每 15s 发 ": ping" 注释行保温。轮询兜底 = GET /downloads。

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// EventHub 事件广播：订阅者收到信号后自行取快照（无锁读 snapshot func）。
type EventHub struct {
	mu       sync.Mutex
	subs     map[chan struct{}]struct{}
	snapshot func() any
}

// NewEventHub snapshot 在写帧时调用，应返回可 json.Marshal 的全量载荷。
func NewEventHub[T any](snapshot func() T) *EventHub {
	return &EventHub{subs: map[chan struct{}]struct{}{}, snapshot: func() any { return snapshot() }}
}

// Signal 非阻塞唤醒全部订阅（engine.OnEvent 接这里；重复信号自动合并）。
func (h *EventHub) Signal() {
	h.mu.Lock()
	for ch := range h.subs {
		select {
		case ch <- struct{}{}:
		default: // 已有待处理信号：合并，不排队
		}
	}
	h.mu.Unlock()
}

func (h *EventHub) subscribe() chan struct{} {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *EventHub) unsubscribe(ch chan struct{}) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

// handleEvents GET /api/v1/events —— text/event-stream。
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "当前连接不支持流式推送", "请改用轮询 GET /api/v1/downloads")
		return
	}
	if s.Events == nil {
		writeError(w, http.StatusServiceUnavailable, "事件流未配置", "请检查服务启动日志；也可用 GET /api/v1/downloads 轮询")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	sub := s.Events.subscribe()
	defer s.Events.unsubscribe(sub)

	send := func() bool {
		payload, err := json.Marshal(s.Events.snapshot())
		if err != nil {
			return true
		}
		if _, err := w.Write([]byte("event: queue\ndata: " + string(payload) + "\n\n")); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	if !send() { // 连接即首帧（免得客户端等第一次变化）
		return
	}

	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-sub:
			if !send() {
				return
			}
		case <-ping.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
