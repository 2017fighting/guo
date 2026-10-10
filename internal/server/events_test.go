package server

// SSE /api/v1/events 用例：连接即收到首帧 queue 事件；队列变化再推一帧；
// 心跳注释不破坏事件流。

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// readEvent 读到下一个 event:<type> + data:<json> 帧（跳过 : ping 注释行）。
func readEvent(t *testing.T, r *bufio.Reader, wantType string, timeout time.Duration) map[string]any {
	t.Helper()
	type result struct {
		payload map[string]any
		err     error
	}
	ch := make(chan result, 1)
	go func() {
		eventType := ""
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				ch <- result{err: err}
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(line, ":") {
				continue // 心跳注释
			}
			if strings.HasPrefix(line, "event:") {
				eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
				continue
			}
			if strings.HasPrefix(line, "data:") {
				if wantType != "" && eventType != wantType {
					continue
				}
				var payload map[string]any
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data:")), &payload); err != nil {
					ch <- result{err: err}
					return
				}
				ch <- result{payload: payload}
				return
			}
		}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			t.Fatalf("读事件失败: %v", res.err)
		}
		return res.payload
	case <-time.After(timeout):
		t.Fatalf("%v 内没等到 %q 事件", timeout, wantType)
		return nil
	}
}

func TestSSEFirstEventWithinTimeout(t *testing.T) {
	f := newQueueFixture(t, queueFixtureOpts{})
	defer f.Close()

	resp, err := http.Get(f.ts.URL + "/api/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content-type = %q", ct)
	}
	r := bufio.NewReader(resp.Body)
	frame := readEvent(t, r, "queue", 2*time.Second)
	if _, ok := frame["jobs"]; !ok {
		t.Fatalf("首帧缺 jobs: %v", frame)
	}
}

func TestSSEPushesOnQueueChange(t *testing.T) {
	f := newQueueFixture(t, queueFixtureOpts{gateFirst: true})
	defer f.Close()

	resp, err := http.Get(f.ts.URL + "/api/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	r := bufio.NewReader(resp.Body)
	readEvent(t, r, "queue", 2*time.Second) // 首帧

	// 队列变化（建任务）→ 在事件流上看到至少 2 帧（jobs 出现且状态推进）
	create := f.postJSON(f.t, "/api/v1/downloads", map[string]any{"series_id": "700002", "episodes": []int{1}})
	create.Body.Close()

	sawRunning := false
	deadline := time.After(5 * time.Second)
	for !sawRunning {
		frameCh := make(chan map[string]any, 1)
		go func() { frameCh <- readEventNoFatal(t, r) }()
		select {
		case frame := <-frameCh:
			jobs, _ := frame["jobs"].([]any)
			for _, j := range jobs {
				m, _ := j.(map[string]any)
				if m["series_id"] == "700002" && (m["status"] == "running" || m["status"] == "queued") {
					sawRunning = true
				}
			}
		case <-deadline:
			t.Fatal("队列变化事件未推送")
		}
	}
}

// readEventNoFatal：轮询用（不把读超时当致命错误）。
func readEventNoFatal(t *testing.T, r *bufio.Reader) map[string]any {
	eventType := ""
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") && eventType == "queue" {
			var payload map[string]any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data:")), &payload); err == nil {
				return payload
			}
		}
	}
}
