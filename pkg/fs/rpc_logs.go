package fs

import (
	"context"
	"encoding/json"
	"strings"
)

func (s *FSHandler) rpcLogs(_ context.Context, params json.RawMessage) (interface{}, error) {
	var p struct {
		Lines  int    `json:"lines"`
		Filter string `json:"filter"`
	}
	if params != nil {
		json.Unmarshal(params, &p)
	}
	if p.Lines == 0 {
		p.Lines = 50
	}
	if p.Lines > 500 {
		p.Lines = 500
	}

	all := s.logBuf.Lines()
	if p.Filter != "" {
		filtered := make([]string, 0, len(all))
		for _, line := range all {
			if strings.Contains(line, p.Filter) {
				filtered = append(filtered, line)
			}
		}
		all = filtered
	}

	// Return last N lines.
	start := len(all) - p.Lines
	if start < 0 {
		start = 0
	}
	lines := all[start:]
	return map[string]interface{}{
		"lines": lines,
		"total": len(all),
	}, nil
}
