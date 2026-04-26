package agent

import "sort"

func mediaAccentInputSchema() map[string]interface{} {
	return objectSchema([]string{"input"}, map[string]interface{}{
		"input": payloadRefSchema("Audio or video file to convert."),
		"target_accent": map[string]interface{}{
			"type":        "string",
			"description": "Requested accent.",
			"default":     "british",
		},
		"save_transcript": map[string]interface{}{
			"type":        "boolean",
			"description": "Whether to save transcript and subtitle artifacts.",
			"default":     false,
		},
	})
}

func mediaAccentOutputSchema() map[string]interface{} {
	return objectSchema([]string{"artifacts"}, map[string]interface{}{
		"summary": map[string]interface{}{"type": "string"},
		"artifacts": map[string]interface{}{
			"type":  "array",
			"items": payloadRefSchema("Generated media, transcript, or subtitle artifact."),
		},
	})
}

func progressStreamSchema() map[string]interface{} {
	return objectSchema(nil, map[string]interface{}{
		"message":  map[string]interface{}{"type": "string"},
		"progress": map[string]interface{}{"type": "number", "minimum": 0, "maximum": 1},
	})
}

func genericInputSchema() map[string]interface{} {
	return objectSchema([]string{"request"}, map[string]interface{}{
		"request": map[string]interface{}{"type": "string"},
	})
}

func genericOutputSchema() map[string]interface{} {
	return objectSchema([]string{"summary"}, map[string]interface{}{
		"summary": map[string]interface{}{"type": "string"},
	})
}

func repositoryTaskInputSchema() map[string]interface{} {
	return objectSchema([]string{"repo", "task"}, map[string]interface{}{
		"repo":        map[string]interface{}{"type": "string"},
		"task":        map[string]interface{}{"type": "string"},
		"branch":      map[string]interface{}{"type": "string"},
		"attachments": map[string]interface{}{"type": "array", "items": payloadRefSchema("Supporting files or issue exports.")},
	})
}

func repositoryTaskOutputSchema() map[string]interface{} {
	return objectSchema([]string{"summary"}, map[string]interface{}{
		"summary":   map[string]interface{}{"type": "string"},
		"branch":    map[string]interface{}{"type": "string"},
		"commit":    map[string]interface{}{"type": "string"},
		"pr_url":    map[string]interface{}{"type": "string"},
		"artifacts": map[string]interface{}{"type": "array", "items": payloadRefSchema("Patch, logs, or generated files.")},
	})
}

func financeResearchInputSchema() map[string]interface{} {
	return objectSchema([]string{"question"}, map[string]interface{}{
		"question":      map[string]interface{}{"type": "string"},
		"tickers":       map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
		"time_horizon":  map[string]interface{}{"type": "string"},
		"payload_refs":  map[string]interface{}{"type": "array", "items": payloadRefSchema("Portfolio, filing, or dataset reference.")},
		"max_sources":   map[string]interface{}{"type": "integer", "minimum": 1},
		"include_risks": map[string]interface{}{"type": "boolean", "default": true},
	})
}

func financeResearchOutputSchema() map[string]interface{} {
	return objectSchema([]string{"memo"}, map[string]interface{}{
		"memo":       map[string]interface{}{"type": "string"},
		"sources":    map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
		"confidence": map[string]interface{}{"type": "string"},
		"artifacts":  map[string]interface{}{"type": "array", "items": payloadRefSchema("Research memo, scratchpad, or data export.")},
	})
}

func payloadRefSchema(description string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "object",
		"description": description,
		"required":    []interface{}{"kind"},
		"properties": map[string]interface{}{
			"kind":      map[string]interface{}{"type": "string"},
			"uri":       map[string]interface{}{"type": "string"},
			"key":       map[string]interface{}{"type": "string"},
			"mime_type": map[string]interface{}{"type": "string"},
			"size":      map[string]interface{}{"type": "integer", "minimum": 0},
			"digest":    map[string]interface{}{"type": "string"},
		},
	}
}

func objectSchema(required []string, properties map[string]interface{}) map[string]interface{} {
	sort.Strings(required)
	return map[string]interface{}{
		"$schema":    "https://json-schema.org/draft/2020-12/schema",
		"type":       "object",
		"required":   required,
		"properties": properties,
	}
}
