// Package llm contains the provider boundary used by the Agent runtime.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"-"`
	Arguments json.RawMessage `json:"-"`
}

type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type CompletionRequest struct {
	Messages []Message        `json:"messages"`
	Tools    []ToolDefinition `json:"tools"`
}

type Completion struct {
	Message Message
}

type Client interface {
	Complete(context.Context, CompletionRequest) (Completion, error)
}

type HTTPClient struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

func NewHTTPClient(baseURL, apiKey, model string) *HTTPClient {
	return &HTTPClient{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, model: model, http: &http.Client{Timeout: 30 * time.Second}}
}

func (c *HTTPClient) Complete(ctx context.Context, in CompletionRequest) (Completion, error) {
	type wireTool struct {
		Type     string `json:"type"`
		Function struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			Parameters  map[string]any `json:"parameters"`
		} `json:"function"`
	}
	type wireMessage struct {
		Role       string `json:"role"`
		Content    string `json:"content,omitempty"`
		ToolCallID string `json:"tool_call_id,omitempty"`
		ToolCalls  []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls,omitempty"`
	}
	request := struct {
		Model    string        `json:"model"`
		Messages []wireMessage `json:"messages"`
		Tools    []wireTool    `json:"tools,omitempty"`
	}{Model: c.model}
	for _, message := range in.Messages {
		item := wireMessage{Role: message.Role, Content: message.Content, ToolCallID: message.ToolCallID}
		for _, call := range message.ToolCalls {
			entry := struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			}{ID: call.ID, Type: "function"}
			entry.Function.Name, entry.Function.Arguments = call.Name, string(call.Arguments)
			item.ToolCalls = append(item.ToolCalls, entry)
		}
		request.Messages = append(request.Messages, item)
	}
	for _, tool := range in.Tools {
		item := wireTool{Type: "function"}
		item.Function.Name, item.Function.Description, item.Function.Parameters = tool.Name, tool.Description, tool.Parameters
		request.Tools = append(request.Tools, item)
	}
	body, err := json.Marshal(request)
	if err != nil {
		return Completion{}, fmt.Errorf("marshal completion: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Completion{}, fmt.Errorf("create completion request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Completion{}, fmt.Errorf("complete: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Completion{}, fmt.Errorf("completion API returned %s", resp.Status)
	}
	var payload struct {
		Choices []struct {
			Message wireMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Completion{}, fmt.Errorf("decode completion: %w", err)
	}
	if len(payload.Choices) == 0 {
		return Completion{}, fmt.Errorf("completion returned no choices")
	}
	answer := Message{Role: "assistant", Content: payload.Choices[0].Message.Content}
	for _, call := range payload.Choices[0].Message.ToolCalls {
		if !json.Valid([]byte(call.Function.Arguments)) {
			return Completion{}, fmt.Errorf("tool %q returned invalid JSON arguments", call.Function.Name)
		}
		answer.ToolCalls = append(answer.ToolCalls, ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: json.RawMessage(call.Function.Arguments)})
	}
	return Completion{Message: answer}, nil
}
