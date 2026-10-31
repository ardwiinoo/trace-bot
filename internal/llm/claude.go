package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type Claude struct {
	apiKey string
	model  string
}

func NewClaude(apiKey, model string) *Claude { return &Claude{apiKey: apiKey, model: model} }

func (c *Claude) Chat(ctx context.Context, system string, msgs []Message, tools []ToolDef) (Message, error) {
	var wire []map[string]any
	for _, m := range msgs {
		switch m.Role {
		case RoleUser:
			wire = append(wire, map[string]any{"role": "user", "content": m.Text})
		case RoleAssistant:
			var blocks []map[string]any
			if m.Text != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": m.Text})
			}
			for _, tc := range m.ToolCalls {
				blocks = append(blocks, map[string]any{
					"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": objectArgs(tc.Args),
				})
			}
			wire = append(wire, map[string]any{"role": "assistant", "content": blocks})
		case RoleTool:
			var blocks []map[string]any
			for _, r := range m.ToolResults {
				blocks = append(blocks, map[string]any{
					"type": "tool_result", "tool_use_id": r.CallID, "content": r.Content, "is_error": r.IsError,
				})
			}
			wire = append(wire, map[string]any{"role": "user", "content": blocks})
		}
	}

	var wireTools []map[string]any
	for _, t := range tools {
		wireTools = append(wireTools, map[string]any{
			"name": t.Name, "description": t.Description, "input_schema": t.Parameters,
		})
	}

	body := map[string]any{
		"model":      c.model,
		"max_tokens": 2048,
		"system":     system,
		"messages":   wire,
	}
	if len(wireTools) > 0 {
		body["tools"] = wireTools
	}

	var resp struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	err := postJSON(ctx, "claude", "https://api.anthropic.com/v1/messages", map[string]string{
		"x-api-key":         c.apiKey,
		"anthropic-version": "2023-06-01",
	}, body, &resp)
	if err != nil {
		return Message{}, err
	}

	out := Message{Role: RoleAssistant}
	var text []string
	for _, b := range resp.Content {
		switch b.Type {
		case "text":
			text = append(text, b.Text)
		case "tool_use":
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: b.ID, Name: b.Name, Args: b.Input})
		}
	}
	out.Text = strings.Join(text, "\n")
	if out.Text == "" && len(out.ToolCalls) == 0 {
		return Message{}, fmt.Errorf("claude: empty response")
	}
	return out, nil
}
