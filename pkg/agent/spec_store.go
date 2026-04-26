package agent

import (
	"bufio"
	"context"
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
	specDirName      = "agents"
	specLogName      = "specs.jsonl"
	defaultSpecLimit = 20
	maxSpecLimit     = 100
)

type SpecStore struct {
	mu      sync.Mutex
	emit    Emitter
	pathFn  func() (string, error)
	now     func() time.Time
	maxLine int
}

type specEntry struct {
	Type    string    `json:"type"`
	SavedAt string    `json:"saved_at"`
	Spec    AgentSpec `json:"spec"`
}

func NewSpecStore(emit Emitter) *SpecStore {
	return &SpecStore{
		emit:    emit,
		pathFn:  specLogPath,
		now:     time.Now,
		maxLine: 16 << 20,
	}
}

func specLogPath() (string, error) {
	root, err := config.RootDir()
	if err != nil {
		return "", fmt.Errorf("finding root directory: %w", err)
	}
	return filepath.Join(root, specDirName, specLogName), nil
}

func (s *SpecStore) Create(ctx context.Context, params AgentSpecCreateParams) (*AgentSpecResult, error) {
	prompt := strings.TrimSpace(params.Prompt)
	if prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	now := s.now().UTC()
	spec := BuildAgentSpec(prompt, now)
	if err := s.save(ctx, spec); err != nil {
		return nil, err
	}
	return &AgentSpecResult{Spec: spec}, nil
}

func (s *SpecStore) Get(_ context.Context, id string) (*AgentSpecResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	specs, err := s.listLatest()
	if err != nil {
		return nil, err
	}
	for _, spec := range specs {
		if spec.ID == id {
			return &AgentSpecResult{Spec: spec}, nil
		}
	}
	return nil, fmt.Errorf("agent spec %q not found", id)
}

func (s *SpecStore) List(_ context.Context, params AgentSpecListParams) (*AgentSpecListResult, error) {
	limit := normalizeSpecLimit(params.Limit)
	status := strings.TrimSpace(params.Status)
	specs, err := s.listLatest()
	if err != nil {
		return nil, err
	}
	result := make([]AgentSpec, 0, min(limit, len(specs)))
	for _, spec := range specs {
		if status != "" && spec.Status != status {
			continue
		}
		result = append(result, spec)
		if len(result) >= limit {
			break
		}
	}
	return &AgentSpecListResult{Specs: result}, nil
}

func (s *SpecStore) Update(ctx context.Context, params AgentSpecUpdateParams) (*AgentSpecResult, error) {
	spec := params.Spec
	spec.ID = strings.TrimSpace(spec.ID)
	if spec.ID == "" {
		return nil, fmt.Errorf("spec.id is required")
	}
	current, err := s.Get(ctx, spec.ID)
	if err != nil {
		return nil, err
	}
	if current.Spec.Status != SpecStatusDraft {
		return nil, fmt.Errorf("%s agent spec %q cannot be updated", current.Spec.Status, spec.ID)
	}
	if status := strings.TrimSpace(spec.Status); status != "" && status != SpecStatusDraft {
		return nil, fmt.Errorf("agent spec %q must be approved or discarded through the action RPC", spec.ID)
	}
	spec.Status = SpecStatusDraft
	spec.CreatedAt = current.Spec.CreatedAt
	spec.ApprovedAt = current.Spec.ApprovedAt
	spec.UpdatedAt = s.now().UTC().Format(time.RFC3339Nano)
	if err := s.save(ctx, spec); err != nil {
		return nil, err
	}
	return &AgentSpecResult{Spec: spec}, nil
}

func (s *SpecStore) Approve(ctx context.Context, id string) (*AgentSpecResult, error) {
	result, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	spec := result.Spec
	if spec.Status == SpecStatusDiscarded {
		return nil, fmt.Errorf("discarded agent spec %q cannot be approved", spec.ID)
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	spec.Status = SpecStatusApproved
	spec.UpdatedAt = now
	spec.ApprovedAt = now
	if err := s.save(ctx, spec); err != nil {
		return nil, err
	}
	return &AgentSpecResult{Spec: spec}, nil
}

func (s *SpecStore) Discard(ctx context.Context, id string) (*AgentSpecResult, error) {
	result, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	spec := result.Spec
	spec.Status = SpecStatusDiscarded
	spec.UpdatedAt = s.now().UTC().Format(time.RFC3339Nano)
	if err := s.save(ctx, spec); err != nil {
		return nil, err
	}
	return &AgentSpecResult{Spec: spec}, nil
}

func (s *SpecStore) save(ctx context.Context, spec AgentSpec) error {
	if err := validateAgentSpec(spec); err != nil {
		return err
	}
	path, err := s.pathFn()
	if err != nil {
		return err
	}
	entry := specEntry{
		Type:    "spec_saved",
		SavedAt: s.now().UTC().Format(time.RFC3339Nano),
		Spec:    spec,
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal agent spec entry: %w", err)
	}
	line = append(line, '\n')

	s.mu.Lock()
	err = func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("create agent spec directory: %w", err)
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return fmt.Errorf("open agent spec log: %w", err)
		}
		defer file.Close()
		if _, err := file.Write(line); err != nil {
			return fmt.Errorf("write agent spec log: %w", err)
		}
		return nil
	}()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if s.emit != nil {
		s.emit("agent.spec.changed", map[string]string{"id": spec.ID, "status": spec.Status})
	}
	return nil
}

func (s *SpecStore) listLatest() ([]AgentSpec, error) {
	path, err := s.pathFn()
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []AgentSpec{}, nil
		}
		return nil, fmt.Errorf("open agent spec log: %w", err)
	}
	defer file.Close()

	byID := map[string]AgentSpec{}
	order := []string{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), s.maxLine)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry specEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Type != "spec_saved" || validateAgentSpec(entry.Spec) != nil {
			continue
		}
		id := entry.Spec.ID
		if _, exists := byID[id]; exists {
			order = slices.DeleteFunc(order, func(value string) bool {
				return value == id
			})
		}
		byID[id] = entry.Spec
		order = append(order, id)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read agent spec log: %w", err)
	}

	specs := make([]AgentSpec, 0, len(order))
	for i := len(order) - 1; i >= 0; i-- {
		specs = append(specs, byID[order[i]])
	}
	return specs, nil
}

func normalizeSpecLimit(limit int) int {
	if limit <= 0 {
		return defaultSpecLimit
	}
	if limit > maxSpecLimit {
		return maxSpecLimit
	}
	return limit
}
