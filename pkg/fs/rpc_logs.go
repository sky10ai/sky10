package fs

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
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

	logPath := "/tmp/sky10/daemon.log"
	f, err := os.Open(logPath)
	if err != nil {
		return nil, fmt.Errorf("opening log: %w", err)
	}
	defer f.Close()

	var all []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if p.Filter != "" && !strings.Contains(line, p.Filter) {
			continue
		}
		all = append(all, line)
	}

	// Return last N lines.
	start := len(all) - p.Lines
	if start < 0 {
		start = 0
	}
	return map[string]interface{}{
		"lines": all[start:],
		"total": len(all),
	}, nil
}
