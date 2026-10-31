// Package tools exposes read-only Loki and Prometheus queries as LLM tools.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"tracebot/internal/llm"
)

const maxOutput = 8000 // chars returned to the LLM per tool call

type Tool struct {
	Def llm.ToolDef
	Run func(ctx context.Context, args json.RawMessage) (string, error)
}

type Registry struct {
	byName map[string]Tool
	order  []Tool
}

type Backends struct {
	LokiURL string
	PromURL string
	Loc     *time.Location
}

type env struct {
	Backends
	http *http.Client
	now  func() time.Time
}

func NewRegistry(b Backends) *Registry {
	e := &env{Backends: b, http: &http.Client{Timeout: 20 * time.Second}, now: time.Now}
	r := &Registry{byName: map[string]Tool{}}
	for _, t := range []Tool{
		e.queryLogsTool(),
		e.queryMetricsTool(),
		e.listLogLabelsTool(),
		e.listMetricsTool(),
		e.describeMetricTool(),
	} {
		r.byName[t.Def.Name] = t
		r.order = append(r.order, t)
	}
	return r
}

func (r *Registry) Defs() []llm.ToolDef {
	defs := make([]llm.ToolDef, len(r.order))
	for i, t := range r.order {
		defs[i] = t.Def
	}
	return defs
}

func (r *Registry) Run(ctx context.Context, name string, args json.RawMessage) (string, error) {
	t, ok := r.byName[name]
	if !ok {
		return "", fmt.Errorf("unknown tool %q", name)
	}
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	out, err := t.Run(ctx, args)
	if err != nil {
		return "", err
	}
	if len(out) > maxOutput {
		out = out[:maxOutput] + "\n... [output truncated]"
	}
	return out, nil
}

// schema helpers

func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func str(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func integer(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}
