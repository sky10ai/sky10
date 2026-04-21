package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// ChatContent is the structured content payload carried over agent chat.
type ChatContent struct {
	Text            string            `json:"text,omitempty"`
	Parts           []ChatContentPart `json:"parts,omitempty"`
	ClientRequestID string            `json:"client_request_id,omitempty"`
}

// ChatContentPart is one chat payload segment.
type ChatContentPart struct {
	Type      string             `json:"type"`
	Text      string             `json:"text,omitempty"`
	Source    *ChatContentSource `json:"source,omitempty"`
	Filename  string             `json:"filename,omitempty"`
	MediaType string             `json:"media_type,omitempty"`
	Caption   string             `json:"caption,omitempty"`
}

// ChatContentSource describes where a media/file part comes from.
type ChatContentSource struct {
	Type      string `json:"type"` // "base64" or "url"
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
	Filename  string `json:"filename,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}

// Normalize returns a canonical content shape without duplicating text when
// callers provide both `text` and explicit text parts.
func (c ChatContent) Normalize() ChatContent {
	out := ChatContent{
		Text:            c.Text,
		Parts:           make([]ChatContentPart, 0, len(c.Parts)),
		ClientRequestID: strings.TrimSpace(c.ClientRequestID),
	}

	hasTextPart := false
	for _, part := range c.Parts {
		normalized := ChatContentPart{
			Type:      strings.TrimSpace(part.Type),
			Text:      part.Text,
			Filename:  strings.TrimSpace(part.Filename),
			MediaType: strings.TrimSpace(part.MediaType),
			Caption:   part.Caption,
		}
		if normalized.Type == "" {
			switch {
			case strings.TrimSpace(part.Text) != "":
				normalized.Type = "text"
			case part.Source != nil:
				normalized.Type = "file"
			default:
				continue
			}
		}
		if part.Source != nil {
			normalized.Source = &ChatContentSource{
				Type:      strings.TrimSpace(part.Source.Type),
				Data:      part.Source.Data,
				URL:       strings.TrimSpace(part.Source.URL),
				Filename:  strings.TrimSpace(part.Source.Filename),
				MediaType: strings.TrimSpace(part.Source.MediaType),
			}
			if normalized.Filename == "" {
				normalized.Filename = normalized.Source.Filename
			}
			if normalized.MediaType == "" {
				normalized.MediaType = normalized.Source.MediaType
			}
		}
		if normalized.Type == "text" {
			if strings.TrimSpace(normalized.Text) == "" {
				continue
			}
			hasTextPart = true
		}
		out.Parts = append(out.Parts, normalized)
	}

	if len(out.Parts) == 0 && strings.TrimSpace(out.Text) != "" {
		out.Parts = []ChatContentPart{{
			Type: "text",
			Text: out.Text,
		}}
		hasTextPart = true
	}
	if strings.TrimSpace(out.Text) == "" && hasTextPart {
		textParts := make([]string, 0, len(out.Parts))
		for _, part := range out.Parts {
			if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
				textParts = append(textParts, part.Text)
			}
		}
		out.Text = strings.Join(textParts, "\n\n")
	}
	if len(out.Parts) == 0 {
		out.Parts = nil
	}
	return out
}

// Validate checks that structured content is safe to forward over the websocket
// contract.
func (c ChatContent) Validate() error {
	normalized := c.Normalize()
	if strings.TrimSpace(normalized.Text) == "" && len(normalized.Parts) == 0 {
		return fmt.Errorf("content is required")
	}

	for _, part := range normalized.Parts {
		switch part.Type {
		case "text":
			if strings.TrimSpace(part.Text) == "" {
				return fmt.Errorf("text part is required")
			}
		case "image", "file", "audio", "video":
			if part.Source == nil {
				return fmt.Errorf("%s part source is required", part.Type)
			}
			switch part.Source.Type {
			case "base64":
				if strings.TrimSpace(part.Source.Data) == "" {
					return fmt.Errorf("%s part base64 source data is required", part.Type)
				}
			case "url":
				if strings.TrimSpace(part.Source.URL) == "" {
					return fmt.Errorf("%s part url source is required", part.Type)
				}
			default:
				return fmt.Errorf("unsupported content source type %q", part.Source.Type)
			}
		default:
			return fmt.Errorf("unsupported content part type %q", part.Type)
		}
	}
	return nil
}

// ParseChatContent accepts legacy text payloads and richer structured content.
func ParseChatContent(raw json.RawMessage) (ChatContent, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ChatContent{}, nil
	}

	var content ChatContent
	objectErr := json.Unmarshal(trimmed, &content)
	if objectErr == nil {
		return content.Normalize(), nil
	}

	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		return ChatContent{
			Text: text,
			Parts: []ChatContentPart{{
				Type: "text",
				Text: text,
			}},
		}, nil
	}

	return ChatContent{}, fmt.Errorf("invalid content JSON: %w", objectErr)
}

// Marshal returns a JSON payload ready for agent transport.
func (c ChatContent) Marshal() (json.RawMessage, error) {
	normalized := c.Normalize()
	if strings.TrimSpace(normalized.Text) == "" && len(normalized.Parts) == 0 {
		return json.RawMessage(`{}`), nil
	}
	body, err := json.Marshal(normalized)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(body), nil
}
