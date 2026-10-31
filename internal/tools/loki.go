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

func (e *env) queryLogsTool() Tool {
	return Tool{
		Def: llm.ToolDef{
			Name: "query_logs",
			Description: "Run a LogQL query against Loki over a time range. Use a log query like " +
				`{service_name="sample-be"} |= "error"` + " to see matching log lines (newest first), or a metric query like " +
				`sum(count_over_time({service_name="sample-be"} |= "error" [5m]))` + " to get counts over time. " +
				"Prefer metric queries to count or find when something happened; use log queries to read examples.",
			Parameters: obj(map[string]any{
				"query": str("LogQL expression"),
				"start": str("Start: RFC3339, 'YYYY-MM-DD HH:MM' in local time, or relative like 'now-6h'. Default now-1h"),
				"end":   str("End, same formats. Default now"),
				"limit": integer("Max log lines for log queries (default 30, max 100)"),
				"step":  str("Resolution for metric queries, e.g. '5m' (default: auto)"),
			}, "query"),
		},
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				Query, Start, End, Step string
				Limit                   int
			}
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
			if a.Limit <= 0 {
				a.Limit = 30
			}
			a.Limit = min(a.Limit, 100)

			p := url.Values{}
			p.Set("query", a.Query)
			p.Set("start", strconv.FormatInt(start.UnixNano(), 10))
			p.Set("end", strconv.FormatInt(end.UnixNano(), 10))
			p.Set("limit", strconv.Itoa(a.Limit))
			p.Set("direction", "backward")
			if a.Step != "" {
				p.Set("step", a.Step)
			}

			var env_ envelope
			if err := e.getJSON(ctx, e.LokiURL, "/loki/api/v1/query_range", p, &env_); err != nil {
				return "", err
			}
			var data struct {
				ResultType string          `json:"resultType"`
				Result     json.RawMessage `json:"result"`
			}
			if err := json.Unmarshal(env_.Data, &data); err != nil {
				return "", err
			}

			header := fmt.Sprintf("range %s to %s\n", e.fmtTime(start), e.fmtTime(end))
			if data.ResultType == "matrix" {
				ss, err := parseMatrix(data.Result)
				if err != nil {
					return "", err
				}
				return header + e.summarizeMatrix(ss), nil
			}
			out, err := e.formatStreams(data.Result, a.Limit)
			if err != nil {
				return "", err
			}
			return header + out, nil
		},
	}
}

func (e *env) formatStreams(raw json.RawMessage, limit int) (string, error) {
	var streams []struct {
		Stream map[string]string `json:"stream"`
		Values [][]string        `json:"values"` // [ns timestamp, line]
	}
	if err := json.Unmarshal(raw, &streams); err != nil {
		return "", err
	}
	type line struct {
		t    time.Time
		svc  string
		text string
	}
	var lines []line
	for _, s := range streams {
		for _, v := range s.Values {
			if len(v) < 2 {
				continue
			}
			ns, _ := strconv.ParseInt(v[0], 10, 64)
			text := v[1]
			if len(text) > 300 {
				text = text[:300] + "..."
			}
			lines = append(lines, line{t: time.Unix(0, ns), svc: s.Stream["service_name"], text: text})
		}
	}
	if len(lines) == 0 {
		return "No log lines matched.", nil
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].t.After(lines[j].t) })
	if len(lines) > limit {
		lines = lines[:limit]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d log lines, newest first (times in %s)", len(lines), e.Loc)
	if len(lines) == limit {
		b.WriteString(" - limit reached, there may be more; narrow the query or use a count_over_time metric query")
	}
	b.WriteString("\n")
	for _, l := range lines {
		fmt.Fprintf(&b, "%s [%s] %s\n", e.fmtTime(l.t), l.svc, l.text)
	}
	return b.String(), nil
}

func (e *env) listLogLabelsTool() Tool {
	return Tool{
		Def: llm.ToolDef{
			Name: "list_log_labels",
			Description: "List Loki label names, or the values of one label (e.g. service_name) seen in the last 24h. " +
				"Call this first when unsure which labels/services exist.",
			Parameters: obj(map[string]any{
				"label": str("If set, return the values of this label instead of the label names"),
			}),
		},
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct{ Label string }
			if err := e.decodeArgs(args, &a); err != nil {
				return "", err
			}
			p := url.Values{}
			p.Set("start", strconv.FormatInt(e.now().Add(-24*time.Hour).UnixNano(), 10))
			p.Set("end", strconv.FormatInt(e.now().UnixNano(), 10))
			path := "/loki/api/v1/labels"
			if a.Label != "" {
				path = "/loki/api/v1/label/" + url.PathEscape(a.Label) + "/values"
			}
			var resp struct {
				Data []string `json:"data"`
			}
			if err := e.getJSON(ctx, e.LokiURL, path, p, &resp); err != nil {
				return "", err
			}
			if len(resp.Data) == 0 {
				return "none found", nil
			}
			return strings.Join(resp.Data, "\n"), nil
		},
	}
}
