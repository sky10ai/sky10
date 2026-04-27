package rootassistant

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sky10/sky10/pkg/config"
)

const (
	historyDirName      = "rootassistant"
	historyLogName      = "runs.jsonl"
	defaultHistoryLimit = 12
	maxHistoryLimit     = 100
)

// Emitter sends daemon events to connected UI clients.
type Emitter func(event string, data interface{})

// Store persists RootAssistant run history as append-only JSONL under the
// sky10 root directory.
type Store struct {
	mu      sync.Mutex
	emit    Emitter
	pathFn  func() (string, error)
	now     func() time.Time
	maxLine int
}

// RunRecord is the durable representation of a RootAssistant run.
type RunRecord struct {
	ID         string      `json:"id"`
	Audience   string      `json:"audience"`
	Prompt     string      `json:"prompt"`
	Answer     string      `json:"answer"`
	Status     string      `json:"status"`
	CreatedAt  string      `json:"createdAt"`
	UpdatedAt  string      `json:"updatedAt"`
	ToolTraces []ToolTrace `json:"toolTraces"`
	FollowUps  []string    `json:"followUps,omitempty"`
}

// ToolTrace mirrors the UI trace schema for persisted RootAssistant runs.
type ToolTrace struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Tool       string `json:"tool"`
	RPCMethod  string `json:"rpcMethod"`
	Status     string `json:"status"`
	Detail     string `json:"detail"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt,omitempty"`
}

// HistoryListParams controls RootAssistant history listing.
type HistoryListParams struct {
	Limit int `json:"limit,omitempty"`
}

// HistoryListResult is returned by rootAssistant.historyList.
type HistoryListResult struct {
	Runs []RunRecord `json:"runs"`
}

// RunSaveParams carries a complete RootAssistant run snapshot to append.
type RunSaveParams struct {
	Run RunRecord `json:"run"`
}

// RunSaveResult is returned by rootAssistant.runSave.
type RunSaveResult struct {
	Status string `json:"status"`
}

type historyEntry struct {
	Type    string    `json:"type"`
	SavedAt string    `json:"savedAt"`
	Run     RunRecord `json:"run"`
}

// NewStore creates a RootAssistant history store.
func NewStore(emit Emitter) *Store {
	return &Store{
		emit:    emit,
		pathFn:  historyLogPath,
		now:     time.Now,
		maxLine: 16 << 20,
	}
}

func historyLogPath() (string, error) {
	root, err := config.RootDir()
	if err != nil {
		return "", fmt.Errorf("finding root directory: %w", err)
	}
	return filepath.Join(root, historyDirName, historyLogName), nil
}

// List returns the latest run snapshot per run ID, newest first.
func (s *Store) List(params HistoryListParams) (*HistoryListResult, error) {
	limit := normalizeHistoryLimit(params.Limit)
	path, err := s.pathFn()
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &HistoryListResult{Runs: []RunRecord{}}, nil
		}
		return nil, fmt.Errorf("open rootAssistant history log: %w", err)
	}
	defer file.Close()

	byID := make(map[string]RunRecord)
	order := make([]string, 0, limit)

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), s.maxLine)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var entry historyEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Type != "run_saved" {
			continue
		}
		if err := validateRun(entry.Run); err != nil {
			continue
		}

		id := entry.Run.ID
		if _, exists := byID[id]; exists {
			order = slices.DeleteFunc(order, func(value string) bool {
				return value == id
			})
		}
		byID[id] = entry.Run
		order = append(order, id)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read rootAssistant history log: %w", err)
	}

	runs := make([]RunRecord, 0, min(limit, len(order)))
	for i := len(order) - 1; i >= 0 && len(runs) < limit; i-- {
		runs = append(runs, byID[order[i]])
	}
	return &HistoryListResult{Runs: runs}, nil
}

// Save appends a complete run snapshot to the JSONL history log.
func (s *Store) Save(params RunSaveParams) (*RunSaveResult, error) {
	if err := validateRun(params.Run); err != nil {
		return nil, err
	}
	path, err := s.pathFn()
	if err != nil {
		return nil, err
	}

	entry := historyEntry{
		Type:    "run_saved",
		SavedAt: s.now().UTC().Format(time.RFC3339Nano),
		Run:     params.Run,
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("marshal rootAssistant history entry: %w", err)
	}
	line = append(line, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create rootAssistant history directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open rootAssistant history log: %w", err)
	}
	defer file.Close()

	if _, err := file.Write(line); err != nil {
		return nil, fmt.Errorf("write rootAssistant history log: %w", err)
	}
	if s.emit != nil {
		s.emit("rootAssistant.history.changed", map[string]string{"id": params.Run.ID})
	}
	return &RunSaveResult{Status: "saved"}, nil
}

func normalizeHistoryLimit(limit int) int {
	if limit <= 0 {
		return defaultHistoryLimit
	}
	if limit > maxHistoryLimit {
		return maxHistoryLimit
	}
	return limit
}

func validateRun(run RunRecord) error {
	if strings.TrimSpace(run.ID) == "" {
		return fmt.Errorf("run.id is required")
	}
	if strings.TrimSpace(run.Prompt) == "" {
		return fmt.Errorf("run.prompt is required")
	}
	switch run.Audience {
	case "for_me", "for_others":
	default:
		return fmt.Errorf("run.audience must be for_me or for_others")
	}
	switch run.Status {
	case "complete", "error", "running":
	default:
		return fmt.Errorf("run.status must be complete, error, or running")
	}
	if err := validateTimestamp("run.createdAt", run.CreatedAt); err != nil {
		return err
	}
	if err := validateTimestamp("run.updatedAt", run.UpdatedAt); err != nil {
		return err
	}
	for i, trace := range run.ToolTraces {
		if strings.TrimSpace(trace.ID) == "" {
			return fmt.Errorf("run.toolTraces[%d].id is required", i)
		}
		if err := validateTimestamp(fmt.Sprintf("run.toolTraces[%d].startedAt", i), trace.StartedAt); err != nil {
			return err
		}
		if trace.FinishedAt != "" {
			if err := validateTimestamp(fmt.Sprintf("run.toolTraces[%d].finishedAt", i), trace.FinishedAt); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateTimestamp(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
		return fmt.Errorf("%s must be RFC3339: %w", field, err)
	}
	return nil
}
