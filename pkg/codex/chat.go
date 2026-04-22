package codex

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
)

const defaultCodexChatModel = "gpt-5.4"

type chatAPIResponse struct {
	ID         string              `json:"id"`
	OutputText string              `json:"output_text,omitempty"`
	Output     []chatAPIOutputItem `json:"output,omitempty"`
	Usage      *chatAPIUsage       `json:"usage,omitempty"`
	Error      *chatAPIErrorBody   `json:"error,omitempty"`
}

type chatAPIOutputItem struct {
	Type    string               `json:"type,omitempty"`
	Role    string               `json:"role,omitempty"`
	Content []chatAPIContentItem `json:"content,omitempty"`
}

type chatAPIContentItem struct {
	Type    string `json:"type,omitempty"`
	Text    string `json:"text,omitempty"`
	Refusal string `json:"refusal,omitempty"`
}

type chatAPIUsage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	TotalTokens  int `json:"total_tokens,omitempty"`
}

type chatAPIErrorResponse struct {
	Error *chatAPIErrorBody `json:"error,omitempty"`
}

type chatAPIErrorBody struct {
	Code    string `json:"code,omitempty"`
	Type    string `json:"type,omitempty"`
	Message string `json:"message,omitempty"`
}

func buildChatInput(messages []ChatMessage) ([]map[string]interface{}, error) {
	if len(messages) == 0 {
		return nil, fmt.Errorf("messages are required")
	}

	input := make([]map[string]interface{}, 0, len(messages))
	for index, message := range messages {
		role := strings.TrimSpace(strings.ToLower(message.Role))
		content := strings.TrimSpace(message.Content)
		if content == "" {
			return nil, fmt.Errorf("message %d is empty", index+1)
		}

		switch role {
		case "user":
			input = append(input, map[string]interface{}{
				"role": "user",
				"content": []map[string]string{
					{
						"type": "input_text",
						"text": content,
					},
				},
			})
		case "assistant":
			input = append(input, map[string]interface{}{
				"type":   "message",
				"role":   "assistant",
				"status": "completed",
				"content": []map[string]interface{}{
					{
						"type":        "output_text",
						"text":        content,
						"annotations": []interface{}{},
					},
				},
			})
		default:
			return nil, fmt.Errorf("message %d has unsupported role %q", index+1, message.Role)
		}
	}

	return input, nil
}

func extractChatText(response chatAPIResponse) string {
	if text := strings.TrimSpace(response.OutputText); text != "" {
		return text
	}

	parts := make([]string, 0, len(response.Output))
	for _, item := range response.Output {
		if item.Type != "message" && item.Role != "assistant" {
			continue
		}
		for _, content := range item.Content {
			switch content.Type {
			case "output_text":
				if text := strings.TrimSpace(content.Text); text != "" {
					parts = append(parts, text)
				}
			case "refusal":
				if text := strings.TrimSpace(content.Refusal); text != "" {
					parts = append(parts, text)
				}
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func parseCodexAPIError(status int, raw []byte) error {
	message := strings.TrimSpace(string(raw))
	if message == "" {
		message = fmt.Sprintf("codex request failed with status %d", status)
	}

	var parsed chatAPIErrorResponse
	if err := json.Unmarshal(raw, &parsed); err == nil && parsed.Error != nil {
		if text := strings.TrimSpace(parsed.Error.Message); text != "" {
			message = text
		} else if code := strings.TrimSpace(parsed.Error.Code); code != "" {
			message = code
		} else if kind := strings.TrimSpace(parsed.Error.Type); kind != "" {
			message = kind
		}
	}

	return fmt.Errorf("codex request failed (%d): %s", status, message)
}

func userAgent() string {
	return fmt.Sprintf("sky10 (%s %s)", runtime.GOOS, runtime.GOARCH)
}
