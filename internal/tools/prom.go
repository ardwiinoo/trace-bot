package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"tracebot/internal/llm"
)

func (e *env) queryMetricsTool() Tool {
	return Tool{
		Def: llm.ToolDef{
			Name: "query_metrics",
			Description: "Run a PromQL range query against Prometheus. Returns per-series min/max/avg, the time of the peak, " +
				"and a downsampled series. Typical use: request rate " + "`sum(rate(<requests_count_metric>[5m]))`" +
				", error rate, latency percentiles with histogram_quantile. Use list_metrics/describe_metric first if unsure of names.",
			Parameters: obj(map[string]any{
				"query": str("PromQL expression"),
				"start": str("Start: RFC3339, 'YYYY-MM-DD HH:MM' in local time, or relative like 'now-6h'. Default now-1h"),
				"end":   str("End, same formats. Default now"),
				"step":  str("Resolution, e.g. '1m', '5m', '1h' (default: range/60)"),
			}, "query"),
		},
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct{ Query, Start, End, Step string }
			if err := e.decodeArgs(args, &a); err != nil {
				return "", err
			}
			if strings.TrimSpace(a.Query) == "" {
				return "", fmt.Errorf("query is required")
			}
			start, end, err := e.timeRange(a.Start, a.End)
			if err != nil {
				return "", err
			}
			step := a.Step
			if step == "" {
				secs := max(int(end.Sub(start).Seconds())/60, 15)
				step = strconv.Itoa(secs) + "s"
			}

			p := url.Values{}
			p.Set("query", a.Query)
			p.Set("start", strconv.FormatInt(start.Unix(), 10))
			p.Set("end", strconv.FormatInt(end.Unix(), 10))
			p.Set("step", step)

			var env_ envelope
			if err := e.getJSON(ctx, e.PromURL, "/api/v1/query_range", p, &env_); err != nil {
				return "", err
			}
			if env_.Status != "success" {
				return "", fmt.Errorf("prometheus: %s", env_.Error)
			}
			var data struct {
				Result json.RawMessage `json:"result"`
			}
			if err := json.Unmarshal(env_.Data, &data); err != nil {
				return "", err
			}
			ss, err := parseMatrix(data.Result)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("range %s to %s, step %s\n%s", e.fmtTime(start), e.fmtTime(end), step, e.summarizeMatrix(ss)), nil
		},
	}
}

func (e *env) listMetricsTool() Tool {
	return Tool{
		Def: llm.ToolDef{
			Name:        "list_metrics",
			Description: "List Prometheus metric names, optionally filtered by a substring (e.g. 'http' or 'duration').",
			Parameters: obj(map[string]any{
				"contains": str("Substring filter, case-insensitive"),
			}),
		},
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct{ Contains string }
			if err := e.decodeArgs(args, &a); err != nil {
				return "", err
			}
			var resp struct {
				Data []string `json:"data"`
			}
			if err := e.getJSON(ctx, e.PromURL, "/api/v1/label/__name__/values", nil, &resp); err != nil {
				return "", err
			}
			var out []string
			for _, n := range resp.Data {
				if a.Contains == "" || strings.Contains(strings.ToLower(n), strings.ToLower(a.Contains)) {
					out = append(out, n)
				}
			}
			if len(out) == 0 {
				return "no matching metrics", nil
			}
			const maxNames = 80
			extra := ""
			if len(out) > maxNames {
				extra = fmt.Sprintf("\n... and %d more, narrow the filter", len(out)-maxNames)
				out = out[:maxNames]
			}
			return strings.Join(out, "\n") + extra, nil
		},
	}
}

func (e *env) describeMetricTool() Tool {
	return Tool{
		Def: llm.ToolDef{
			Name: "describe_metric",
			Description: "Show the label names and example label values of a metric (from the last hour), " +
				"so you can write correct PromQL selectors and group-by clauses.",
			Parameters: obj(map[string]any{
				"metric": str("Exact metric name"),
			}, "metric"),
		},
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct{ Metric string }
			if err := e.decodeArgs(args, &a); err != nil {
				return "", err
			}
			p := url.Values{}
			p.Add("match[]", a.Metric)
			p.Set("start", strconv.FormatInt(e.now().Add(-time.Hour).Unix(), 10))
			p.Set("end", strconv.FormatInt(e.now().Unix(), 10))
			var resp struct {
				Data []map[string]string `json:"data"`
			}
			if err := e.getJSON(ctx, e.PromURL, "/api/v1/series", p, &resp); err != nil {
				return "", err
			}
			if len(resp.Data) == 0 {
				return "no series found for this metric in the last hour", nil
			}
			values := map[string]map[string]bool{}
			for _, s := range resp.Data {
				for k, v := range s {
					if k == "__name__" {
						continue
					}
					if values[k] == nil {
						values[k] = map[string]bool{}
					}
					values[k][v] = true
				}
			}
			keys := make([]string, 0, len(values))
			for k := range values {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			var b strings.Builder
			fmt.Fprintf(&b, "%s: %d series\n", a.Metric, len(resp.Data))
			for _, k := range keys {
				vs := make([]string, 0, len(values[k]))
				for v := range values[k] {
					vs = append(vs, v)
				}
				sort.Strings(vs)
				more := ""
				if len(vs) > 8 {
					more = fmt.Sprintf(" ... (%d distinct)", len(vs))
					vs = vs[:8]
				}
				fmt.Fprintf(&b, "  %s: %s%s\n", k, strings.Join(vs, ", "), more)
			}
			return b.String(), nil
		},
	}
}
