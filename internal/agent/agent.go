// Package agent runs the LLM tool-calling loop and keeps per-session history.
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"tracebot/internal/llm"
	"tracebot/internal/tools"
)

const (
	maxSteps    = 8  // LLM round trips per question
	maxHistory  = 12 // messages kept per session (question/answer pairs only)
	askDeadline = 2 * time.Minute
)

type Query struct {
	Tool string `json:"tool"`
	Args string `json:"args"`
}

type Result struct {
	Answer  string  `json:"answer"`
	Queries []Query `json:"queries"` // every tool call made, for transparency
}

type session struct {
	mu      sync.Mutex
	history []llm.Message
}

type Agent struct {
	llm   llm.Client
	tools *tools.Registry
	loc   *time.Location

	mu       sync.Mutex
	sessions map[string]*session
}

func New(c llm.Client, t *tools.Registry, loc *time.Location) *Agent {
	return &Agent{llm: c, tools: t, loc: loc, sessions: map[string]*session{}}
}

func (a *Agent) session(id string) *session {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[id]
	if !ok {
		s = &session{}
		a.sessions[id] = s
	}
	return s
}

// Reset clears the conversation history of a session.
func (a *Agent) Reset(id string) {
	s := a.session(id)
	s.mu.Lock()
	s.history = nil
	s.mu.Unlock()
}

func (a *Agent) systemPrompt() string {
	now := time.Now().In(a.loc)
	return fmt.Sprintf(`You are Trace Bot, an assistant that answers questions about the health of backend services by querying their observability data (Grafana stack: Loki for logs, Prometheus for metrics).

Current time: %s (%s, weekday %s). Always interpret times the user mentions ("jam 2 pagi", "kemarin", "tadi malam") in this timezone, and pass absolute times like '%s' or relative ones like 'now-6h' to the tools.

Environment notes (from the current deployment; verify with the list_* tools only if a query unexpectedly returns nothing):
- Loki: every log has the label service_name (currently "sample-be"). Per-log structured metadata can be filtered in the pipeline: detected_level (debug/info/warn/error), severity_text, path (request path), trace_id. Select errors with | detected_level="error", never with a text match like |= "error". Count with count_over_time and group with sum by (path) (...).
- Prometheus: HTTP server metrics come from OpenTelemetry: http_server_request_duration_seconds_{count,sum,bucket}; use describe_metric to see its labels (route, status code, method). Request rate: sum(rate(http_server_request_duration_seconds_count[5m])); latency percentile: histogram_quantile(0.95, sum by (le) (rate(http_server_request_duration_seconds_bucket[5m]))).

Rules:
- Answer using tool results only. Never invent numbers, log lines or times. If a query returns nothing, say so plainly and, if sensible, check the label/metric names before concluding there is no data.
- If you are not sure which services, labels or metric names exist, call list_log_labels / list_metrics / describe_metric first instead of guessing.
- If a query returns an error, read the message, fix the query and retry.
- Prefer aggregated queries (count_over_time, rate, histogram_quantile) over reading many raw log lines. Read raw lines only to show example errors.
- Reply in the user's language (Indonesian or English), short and concrete: lead with the answer, then the key numbers and times in %s. Plain text only, no markdown tables.`,
		now.Format("2006-01-02 15:04:05 -07:00"), a.loc, now.Weekday(), now.Add(-6*time.Hour).Format(time.RFC3339), a.loc)
}

// Ask answers one question within a session.
func (a *Agent) Ask(ctx context.Context, sessionID, question string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, askDeadline)
	defer cancel()

	s := a.session(sessionID)
	s.mu.Lock()
	defer s.mu.Unlock()

	msgs := append(append([]llm.Message{}, s.history...), llm.Message{Role: llm.RoleUser, Text: question})
	system := a.systemPrompt()
	defs := a.tools.Defs()

	var res Result
	for step := 0; step < maxSteps; step++ {
		reply, err := a.llm.Chat(ctx, system, msgs, defs)
		if err != nil {
			return res, err
		}
		msgs = append(msgs, reply)
		if len(reply.ToolCalls) == 0 {
			res.Answer = strings.TrimSpace(reply.Text)
			break
		}

		results := llm.Message{Role: llm.RoleTool}
		for _, tc := range reply.ToolCalls {
			res.Queries = append(res.Queries, Query{Tool: tc.Name, Args: string(tc.Args)})
			out, err := a.tools.Run(ctx, tc.Name, tc.Args)
			tr := llm.ToolResult{CallID: tc.ID, Name: tc.Name, Content: out}
			if err != nil {
				tr.Content, tr.IsError = err.Error(), true
			}
			slog.Info("tool call", "tool", tc.Name, "args", string(tc.Args), "error", tr.IsError)
			results.ToolResults = append(results.ToolResults, tr)
		}
		msgs = append(msgs, results)
	}
	if res.Answer == "" {
		res.Answer = "Maaf, saya tidak berhasil menyimpulkan jawaban dalam batas langkah yang diizinkan. Coba pertanyaan yang lebih spesifik."
	}

	// Keep only the question and final answer; tool traffic is not worth the tokens.
	s.history = append(s.history,
		llm.Message{Role: llm.RoleUser, Text: question},
		llm.Message{Role: llm.RoleAssistant, Text: res.Answer})
	if len(s.history) > maxHistory {
		s.history = s.history[len(s.history)-maxHistory:]
	}
	return res, nil
}
