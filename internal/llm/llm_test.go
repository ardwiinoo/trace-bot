package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDailyQuotaFailsFastWithoutRetry(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, `{"error":{"code":429,"message":"Generate content requests per day limit exceeded on Free Tier"}}`, 429)
	}))
	defer srv.Close()

	var out struct{}
	start := time.Now()
	err := postJSON(context.Background(), "gemini", srv.URL, nil, map[string]string{}, &out)
	if err == nil || !strings.Contains(err.Error(), "kuota harian") {
		t.Fatalf("expected daily quota error, got %v", err)
	}
	if hits.Load() != 1 || time.Since(start) > time.Second {
		t.Errorf("should not retry a daily quota: hits=%d elapsed=%v", hits.Load(), time.Since(start))
	}
}

func TestRetryAfterReadsGeminiRetryDelay(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	got := retryAfter(resp, []byte(`{"details":[{"retryDelay":"23s"}]}`))
	if got != 24*time.Second {
		t.Errorf("retryAfter = %v, want 24s", got)
	}
	resp.Header.Set("Retry-After", "5")
	if got := retryAfter(resp, nil); got != 6*time.Second {
		t.Errorf("retryAfter header = %v, want 6s", got)
	}
}
