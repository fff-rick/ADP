package api

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"adp/internal/domain/model"
)

type contextRegressionCase struct {
	ID    string `json:"id"`
	Turns []struct {
		User      string         `json:"user"`
		Assistant string         `json:"assistant"`
		ToolName  string         `json:"tool_name"`
		ToolData  map[string]any `json:"tool_data"`
	} `json:"turns"`
	RequiredContext []string `json:"required_context"`
}

func TestContextCompressionRegressionCases(t *testing.T) {
	body, err := os.ReadFile("testdata/context_regression_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []contextRegressionCase
	if err := json.Unmarshal(body, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.ID, func(t *testing.T) {
			var messages []model.ConversationMessage
			for index, turn := range tc.Turns {
				messages = append(messages, model.ConversationMessage{Role: "user", Content: turn.User})
				if turn.ToolName != "" {
					messages = append(messages, model.ConversationMessage{Role: "tool", ToolName: turn.ToolName, ToolData: turn.ToolData, Step: index + 1})
				}
				messages = append(messages, model.ConversationMessage{Role: "assistant", Content: turn.Assistant})
			}
			var projection strings.Builder
			for _, message := range conversationMessagesToLLM(messages) {
				projection.WriteString(message.Content)
				projection.WriteByte('\n')
			}
			for _, required := range tc.RequiredContext {
				if !strings.Contains(projection.String(), required) {
					t.Fatalf("compressed context lost required evidence %q: %s", required, projection.String())
				}
			}
		})
	}
}
