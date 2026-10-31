package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"tracebot/internal/llm"
	"tracebot/internal/tools"
)

// scripted replies with a tool call first, then a final answer that must
// have seen the tool result.
type scripted struct {
	calls int
	saw   string
}

func (s *scripted) Chat(_ context.Context, _ string, msgs []llm.Message, _ []llm.ToolDef) (llm.Message, error) {
	s.calls++
	if s.calls == 1 {
		return llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "1", Name: "list_log_labels", Args: json.RawMessage(`{}`)},
		}}, nil
	}
	last := msgs[len(msgs)-1]
	if last.Role == llm.RoleTool {
		s.saw = last.ToolResults[0].Content
	}
	return llm.Message{Role: llm.RoleAssistant, Text: "done"}, nil
}

func TestAskRunsToolsThenAnswers(t *testing.T) {
	// Backend is unreachable on purpose: the error must reach the LLM as a tool error, not abort the loop.
	reg := tools.NewRegistry(tools.Backends{LokiURL: "http://127.0.0.1:1", PromURL: "http://127.0.0.1:1", Loc: time.UTC})
	llmc := &scripted{}
	ag := New(llmc, reg, time.UTC)

	res, err := ag.Ask(context.Background(), "s1", "ada error?")
	if err != nil {
		t.Fatal(err)
	}
	if res.Answer != "done" || len(res.Queries) != 1 || res.Queries[0].Tool != "list_log_labels" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if llmc.saw == "" {
		t.Fatal("LLM never received the tool result")
	}

	// History keeps only the question/answer pair.
	if got := len(ag.session("s1").history); got != 2 {
		t.Errorf("history len = %d, want 2", got)
	}
	ag.Reset("s1")
	if got := len(ag.session("s1").history); got != 0 {
		t.Errorf("history after reset = %d", got)
	}
}
