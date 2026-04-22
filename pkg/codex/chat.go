package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

import (
	"runtime"
)

const defaultCodexChatModel = "gpt-5.4"
const defaultCodexInstructions = "You are Codex inside sky10. Help with coding tasks and answer directly."

type chatAPIResponse struct {
	ID         string              `json:"id"`
	Model      string              `json:"model,omitempty"`
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

type chatAPIStreamEvent struct {
	Type     string            `json:"type,omitempty"`
	Delta    string            `json:"delta,omitempty"`
	Text     string            `json:"text,omitempty"`
	Response *chatAPIResponse  `json:"response,omitempty"`
	Error    *chatAPIErrorBody `json:"error,omitempty"`
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

func parseCodexAPIStream(raw io.Reader, fallbackModel string) (*ChatResult, error) {
	scanner := bufio.NewScanner(raw)
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)

	result := &ChatResult{Model: fallbackModel}
	var textBuilder strings.Builder
	var eventName string
	var dataLines []string

	flush := func() error {
		if len(dataLines) == 0 {
			eventName = ""
			return nil
		}

		payload := strings.TrimSpace(strings.Join(dataLines, "\n"))
		eventName = ""
		dataLines = nil

		if payload == "" || payload == "[DONE]" {
			return nil
		}

		var event chatAPIStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return fmt.Errorf("decode codex stream event: %w", err)
		}

		switch event.Type {
		case "response.output_text.delta":
			textBuilder.WriteString(event.Delta)
		case "response.output_text.done":
			if textBuilder.Len() == 0 && strings.TrimSpace(event.Text) != "" {
				textBuilder.WriteString(event.Text)
			}
		case "response.completed":
			if event.Response != nil {
				result.ResponseID = event.Response.ID
				if strings.TrimSpace(event.Response.Model) != "" {
					result.Model = event.Response.Model
				}
				if event.Response.Usage != nil {
					result.Usage = &ChatUsage{
						InputTokens:  event.Response.Usage.InputTokens,
						OutputTokens: event.Response.Usage.OutputTokens,
						TotalTokens:  event.Response.Usage.TotalTokens,
					}
				}
			}
		case "response.failed", "response.error", "error":
			if event.Error != nil {
				return fmt.Errorf("codex stream failed: %s", strings.TrimSpace(event.Error.Message))
			}
			if event.Response != nil && event.Response.Error != nil {
				return fmt.Errorf("codex stream failed: %s", strings.TrimSpace(event.Response.Error.Message))
			}
			if eventName != "" {
				return fmt.Errorf("codex stream failed during %s", eventName)
			}
			return fmt.Errorf("codex stream failed")
		}

		return nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read codex stream: %w", err)
	}
	if err := flush(); err != nil {
		return nil, err
	}

	result.Text = strings.TrimSpace(textBuilder.String())
	if result.Text == "" {
		return nil, fmt.Errorf("codex returned an empty response")
	}
	return result, nil
}

func userAgent() string {
	return fmt.Sprintf("sky10 (%s %s)", runtime.GOOS, runtime.GOARCH)
}
