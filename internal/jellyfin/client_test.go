package jellyfin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func newTestServer(t *testing.T, status int, hits *int32, check func(r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		if check != nil {
			check(r)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestValidateUsesAuthorizationHeader(t *testing.T) {
	var hits int32
	srv := newTestServer(t, 200, &hits, func(r *http.Request) {
		if got := r.Header.Get("Authorization"); got != `MediaBrowser Token="k3y"` {
			t.Errorf("Authorization = %q", got)
		}
		if r.URL.Path != "/System/Info" {
			t.Errorf("path = %q", r.URL.Path)
		}
	})
	c := Client{BaseURL: srv.URL, APIKey: "k3y"}
	if err := c.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("hits = %d", hits)
	}
}

func TestValidateRejectsBadStatus(t *testing.T) {
	var hits int32
	srv := newTestServer(t, 401, &hits, nil)
	c := Client{BaseURL: srv.URL, APIKey: "bad"}
	if err := c.Validate(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestRefreshAsyncPostsAndReports(t *testing.T) {
	var hits int32
	srv := newTestServer(t, 204, &hits, func(r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/Library/Refresh" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	errs := make(chan string, 1)
	c := Client{BaseURL: srv.URL, APIKey: "k3y", OnError: func(m string) { errs <- m }}
	c.RefreshAsync()

	deadline := time.After(3 * time.Second)
	for atomic.LoadInt32(&hits) == 0 {
		select {
		case m := <-errs:
			t.Fatalf("unexpected error: %s", m)
		case <-deadline:
			t.Fatal("refresh never fired")
		default:
		}
	}
	select {
	case m := <-errs:
		t.Fatalf("unexpected error: %s", m)
	default:
	}
}

func TestRefreshAsyncAuthFailureReportedNotReturned(t *testing.T) {
	var hits int32
	srv := newTestServer(t, 403, &hits, nil)
	errs := make(chan string, 1)
	c := Client{BaseURL: srv.URL, APIKey: "k3y", OnError: func(m string) { errs <- m }}
	c.RefreshAsync()
	select {
	case m := <-errs:
		if hits == 0 {
			t.Fatal("no request fired")
		}
		// 只上报不抛错即通过
		_ = m
	case <-time.After(3 * time.Second):
		t.Fatal("error not reported")
	}
}

func TestDisabledClientIsNoop(t *testing.T) {
	c := Client{}
	c.RefreshAsync() // 不应 panic / 不发请求
	if c.Enabled() {
		t.Fatal("empty client should be disabled")
	}
	if err := c.Validate(context.Background()); err == nil {
		t.Fatal("disabled validate should error")
	}
}
