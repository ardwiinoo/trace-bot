package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type Gemini struct {
	apiKey string
	model  string
}

func NewGemini(apiKey, model string) *Gemini { return &Gemini{apiKey: apiKey, model: model} }

func (g *Gemini) Chat(ctx context.Context, system string, msgs []Message, tools []ToolDef) (Message, error) {
	var contents []map[string]any
	for _, m := range msgs {
		switch m.Role {
		case RoleUser:
			contents = append(contents, map[string]any{
				"role": "user", "parts": []map[string]any{{"text": m.Text}},
			})
		case RoleAssistant:
			if len(m.Raw) > 0 {
				contents = append(contents, map[string]any{"role": "model", "parts": m.Raw})
				continue
			}
			var parts []map[string]any
			if m.Text != "" {
				parts = append(parts, map[string]any{"text": m.Text})
			}
			for _, tc := range m.ToolCalls {
				parts = append(parts, map[string]any{
					"functionCall": map[string]any{"name": tc.Name, "args": objectArgs(tc.Args)},
				})
			}
			contents = append(contents, map[string]any{"role": "model", "parts": parts})
		case RoleTool:
			var parts []map[string]any
			for _, r := range m.ToolResults {
				key := "result"
				if r.IsError {
					key = "error"
				}
				parts = append(parts, map[string]any{
					"functionResponse": map[string]any{
						"name": r.Name, "response": map[string]any{key: r.Content},
					},
				})
			}
			contents = append(contents, map[string]any{"role": "user", "parts": parts})
		}
	}

	body := map[string]any{
		"systemInstruction": map[string]any{"parts": []map[string]any{{"text": system}}},
		"contents":          contents,
		"generationConfig":  map[string]any{"maxOutputTokens": 2048},
	}
	if len(tools) > 0 {
		var decls []map[string]any
		for _, t := range tools {
			decls = append(decls, map[string]any{
				"name": t.Name, "description": t.Description, "parameters": t.Parameters,
			})
		}
		body["tools"] = []map[string]any{{"functionDeclarations": decls}}
	}

	var resp struct {
		Candidates []struct {
			Content struct {
				Parts []json.RawMessage `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
	}
	url := "https://generativelanguage.googleapis.com/v1beta/models/" + g.model + ":generateContent"
	if err := postJSON(ctx, "gemini", url, map[string]string{"x-goog-api-key": g.apiKey}, body, &resp); err != nil {
		return Message{}, err
	}
	if len(resp.Candidates) == 0 {
		return Message{}, fmt.Errorf("gemini: no candidates (block reason %q)", resp.PromptFeedback.BlockReason)
	}
	cand := resp.Candidates[0]

	out := Message{Role: RoleAssistant}
	var text []string
	for i, raw := range cand.Content.Parts {
		var p struct {
			Text         string `json:"text"`
			Thought      bool   `json:"thought"`
			FunctionCall *struct {
				Name string          `json:"name"`
				Args json.RawMessage `json:"args"`
			} `json:"functionCall"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return Message{}, err
		}
		switch {
		case p.FunctionCall != nil:
			out.ToolCalls = append(out.ToolCalls, ToolCall{
				ID: fmt.Sprintf("%s-%d", p.FunctionCall.Name, i), Name: p.FunctionCall.Name, Args: p.FunctionCall.Args,
			})
		case p.Text != "" && !p.Thought:
			text = append(text, p.Text)
		}
	}
	out.Text = strings.Join(text, "\n")
	if len(out.ToolCalls) > 0 {
		// Replay the parts untouched so thought signatures survive the round trip.
		out.Raw, _ = json.Marshal(cand.Content.Parts)
	}
	if out.Text == "" && len(out.ToolCalls) == 0 {
		return Message{}, fmt.Errorf("gemini: empty response (finish reason %q)", cand.FinishReason)
	}
	return out, nil
}
