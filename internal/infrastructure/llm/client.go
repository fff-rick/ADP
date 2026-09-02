// Package llm contains the provider boundary used by the Agent runtime.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	Usage   Usage
}

type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

type Client interface {
	Complete(context.Context, CompletionRequest) (Completion, error)
}

// EmbeddingClient is deliberately separate from Client: chat access does not
// imply permission to export reviewed incident knowledge to an embedding API.
type EmbeddingClient interface {
	Embed(context.Context, string) ([]float32, error)
}

type HTTPEmbeddingClient struct {
	baseURL, apiKey, model string
	dimensions             int
	http                   *http.Client
}

func NewHTTPEmbeddingClient(baseURL, apiKey, model string, dimensions int) *HTTPEmbeddingClient {
	return &HTTPEmbeddingClient{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, model: model, dimensions: dimensions, http: &http.Client{Timeout: 30 * time.Second}}
}

func (c *HTTPEmbeddingClient) Embed(ctx context.Context, input string) ([]float32, error) {
	if strings.TrimSpace(input) == "" {
		return nil, errors.New("embedding input is required")
	}
	body, err := json.Marshal(map[string]any{"model": c.model, "input": input, "dimensions": c.dimensions})
	if err != nil {
		return nil, fmt.Errorf("marshal embedding: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create embedding request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embedding API returned %s", resp.Status)
	}
	var payload struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode embedding: %w", err)
	}
	if len(payload.Data) != 1 || len(payload.Data[0].Embedding) != c.dimensions {
		return nil, fmt.Errorf("embedding returned %d dimensions, want %d", len(payload.Data[0].Embedding), c.dimensions)
	}
	return payload.Data[0].Embedding, nil
}

// StreamingClient is implemented by providers that can deliver assistant text
// incrementally. Tool calls are still assembled before the runtime executes
// them, so the authorization boundary remains unchanged.
type StreamingClient interface {
	CompleteStream(context.Context, CompletionRequest, func(string) error) (Completion, error)
}

type HTTPClient struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

func NewHTTPClient(baseURL, apiKey, model string) *HTTPClient {
	return &HTTPClient{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, model: model, http: &http.Client{Timeout: 120 * time.Second}}
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
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
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
	return Completion{Message: answer, Usage: Usage{PromptTokens: payload.Usage.PromptTokens, CompletionTokens: payload.Usage.CompletionTokens, TotalTokens: payload.Usage.TotalTokens}}, nil
}

// CompleteStream calls the OpenAI-compatible SSE endpoint and invokes onDelta
// for each text fragment as it arrives. DeepSeek and OpenAI both use this
// shape for chat-completions streaming.
func (c *HTTPClient) CompleteStream(ctx context.Context, in CompletionRequest, onDelta func(string) error) (Completion, error) {
	type wireTool struct {
		Type     string `json:"type"`
		Function struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			Parameters  map[string]any `json:"parameters"`
		} `json:"function"`
	}
	type wireCall struct {
		Index    int    `json:"index"`
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	type wireMessage struct {
		Role       string     `json:"role"`
		Content    string     `json:"content,omitempty"`
		ToolCallID string     `json:"tool_call_id,omitempty"`
		ToolCalls  []wireCall `json:"tool_calls,omitempty"`
	}
	request := struct {
		Model    string        `json:"model"`
		Messages []wireMessage `json:"messages"`
		Tools    []wireTool    `json:"tools,omitempty"`
		Stream   bool          `json:"stream"`
	}{Model: c.model, Stream: true}
	for _, message := range in.Messages {
		item := wireMessage{Role: message.Role, Content: message.Content, ToolCallID: message.ToolCallID}
		for index, call := range message.ToolCalls {
			entry := wireCall{Index: index, ID: call.ID, Type: "function"}
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
		return Completion{}, fmt.Errorf("marshal streaming completion: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Completion{}, fmt.Errorf("create streaming completion request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Completion{}, fmt.Errorf("stream completion: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Completion{}, fmt.Errorf("streaming completion API returned %s", resp.Status)
	}

	var content strings.Builder
	type assembledCall struct {
		id, name  string
		arguments strings.Builder
	}
	calls := make(map[int]*assembledCall)
	usage := Usage{}
	reader := bufio.NewReader(resp.Body)
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return Completion{}, fmt.Errorf("read completion stream: %w", readErr)
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				break
			}
			var payload struct {
				Choices []struct {
					Delta struct {
						Content   string     `json:"content"`
						ToolCalls []wireCall `json:"tool_calls"`
					} `json:"delta"`
				} `json:"choices"`
				Usage struct {
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
					TotalTokens      int `json:"total_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal([]byte(data), &payload); err != nil {
				return Completion{}, fmt.Errorf("decode completion stream: %w", err)
			}
			if payload.Usage.TotalTokens != 0 {
				usage = Usage{PromptTokens: payload.Usage.PromptTokens, CompletionTokens: payload.Usage.CompletionTokens, TotalTokens: payload.Usage.TotalTokens}
			}
			for _, choice := range payload.Choices {
				if choice.Delta.Content != "" {
					content.WriteString(choice.Delta.Content)
					if onDelta != nil {
						if err := onDelta(choice.Delta.Content); err != nil {
							return Completion{}, err
						}
					}
				}
				for _, delta := range choice.Delta.ToolCalls {
					call := calls[delta.Index]
					if call == nil {
						call = &assembledCall{}
						calls[delta.Index] = call
					}
					if delta.ID != "" {
						call.id = delta.ID
					}
					if delta.Function.Name != "" {
						call.name = delta.Function.Name
					}
					call.arguments.WriteString(delta.Function.Arguments)
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	answer := Message{Role: "assistant", Content: content.String()}
	for index := 0; ; index++ {
		call, ok := calls[index]
		if !ok {
			break
		}
		arguments := call.arguments.String()
		if call.id == "" || call.name == "" || !json.Valid([]byte(arguments)) {
			return Completion{}, fmt.Errorf("stream returned invalid tool call at index %d", index)
		}
		answer.ToolCalls = append(answer.ToolCalls, ToolCall{ID: call.id, Name: call.name, Arguments: json.RawMessage(arguments)})
	}
	return Completion{Message: answer, Usage: usage}, nil
}
