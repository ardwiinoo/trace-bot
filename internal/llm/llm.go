// Package llm defines a small provider-neutral chat interface with tool calling.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool" // carries the results of the previous assistant tool calls
)

type ToolDef struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON schema (object)
}

type ToolCall struct {
	ID   string
	Name string
	Args json.RawMessage
}

type ToolResult struct {
	CallID  string
	Name    string
	Content string
	IsError bool
}

type Message struct {
	Role        Role
	Text        string
	ToolCalls   []ToolCall
	ToolResults []ToolResult
	// Raw holds provider-specific assistant content that must be replayed
	// verbatim on the next request (e.g. Gemini thought signatures).
	Raw json.RawMessage
}

type Client interface {
	Chat(ctx context.Context, system string, msgs []Message, tools []ToolDef) (Message, error)
}

var httpClient = &http.Client{Timeout: 90 * time.Second}

// postJSON sends body as JSON and decodes the response into out. It retries
// on 429/5xx, which both providers return regularly under load.
func postJSON(ctx context.Context, name, url string, headers map[string]string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	var lastErr error
	wait := time.Duration(0)
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			if wait == 0 {
				wait = time.Duration(attempt) * 2 * time.Second
			}
			slog.Warn("llm retry", "provider", name, "attempt", attempt, "wait", wait, "last_error", lastErr)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return ctx.Err()
			}
			wait = 0
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", name, err)
			continue
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests && isDailyQuota(data) {
			// Waiting a minute cannot fix a daily quota, so fail fast with a clear message.
			return fmt.Errorf("%s: kuota harian free tier habis (coba lagi besok, aktifkan billing, ganti GEMINI_MODEL, atau set LLM_PROVIDER=claude)", name)
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("%s: HTTP %d: %s", name, resp.StatusCode, snippet(data))
			wait = retryAfter(resp, data)
			continue
		}
		if resp.StatusCode/100 != 2 {
			return fmt.Errorf("%s: HTTP %d: %s", name, resp.StatusCode, snippet(data))
		}
		return json.Unmarshal(data, out)
	}
	return lastErr
}

// isDailyQuota reports whether a 429 body is about a per-day quota.
func isDailyQuota(body []byte) bool {
	s := strings.ToLower(string(body))
	return strings.Contains(s, "per day") || strings.Contains(s, "perday")
}

var retryDelayRe =regexp.MustCompile(`"retryDelay":\s*"(\d+(?:\.\d+)?)s"`)

// retryAfter reads how long the provider asks us to back off (Retry-After
// header, or Gemini's retryDelay in the error body), capped at 60s. Free-tier
// per-minute quotas are routine, so waiting is better than failing.
func retryAfter(resp *http.Response, body []byte) time.Duration {
	var d time.Duration
	if n, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
		d = time.Duration(n) * time.Second
	} else if m := retryDelayRe.FindSubmatch(body); m != nil {
		f, _ := strconv.ParseFloat(string(m[1]), 64)
		d = time.Duration(f * float64(time.Second))
	}
	if d > 0 {
		d += time.Second
	}
	return min(d, 60*time.Second)
}

func snippet(b []byte) string {
	if len(b) > 500 {
		b = b[:500]
	}
	return string(b)
}

func objectArgs(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage("{}")
	}
	return raw
}
