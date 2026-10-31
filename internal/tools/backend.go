package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// getJSON calls a Loki/Prometheus HTTP API. Non-2xx responses become errors
// carrying the backend's message, so the LLM can read it and fix its query.
func (e *env) getJSON(ctx context.Context, base, path string, params url.Values, out any) error {
	u := base + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := e.http.Do(req)
	if err != nil {
		return fmt.Errorf("backend unreachable: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 600 {
			msg = msg[:600]
		}
		return fmt.Errorf("query failed (HTTP %d): %s", resp.StatusCode, msg)
	}
	return json.Unmarshal(data, out)
}

// envelope is the common {"status","data"} wrapper of Prometheus and Loki.
type envelope struct {
	Status string          `json:"status"`
	Error  string          `json:"error"`
	Data   json.RawMessage `json:"data"`
}

func (e *env) decodeArgs(args json.RawMessage, dst any) error {
	if err := json.Unmarshal(args, dst); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}
