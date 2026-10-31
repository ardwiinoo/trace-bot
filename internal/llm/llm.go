// Package llm defines a small provider-neutral chat interface with tool calling.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			case <-ctx.Done():
				return ctx.Err()
			}
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
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("%s: HTTP %d: %s", name, resp.StatusCode, snippet(data))
			continue
		}
		if resp.StatusCode/100 != 2 {
			return fmt.Errorf("%s: HTTP %d: %s", name, resp.StatusCode, snippet(data))
		}
		return json.Unmarshal(data, out)
	}
	return lastErr
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
