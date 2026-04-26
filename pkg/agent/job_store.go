package agent

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	jobLogName      = "jobs.jsonl"
	defaultJobLimit = 50
	maxJobLimit     = 200
)

type JobStore struct {
	mu      sync.Mutex
	emit    Emitter
	pathFn  func() (string, error)
	now     func() time.Time
	maxLine int
}

type jobEntry struct {
	Type    string   `json:"type"`
	SavedAt string   `json:"saved_at"`
	Job     AgentJob `json:"job"`
}

func NewJobStore(emit Emitter) *JobStore {
	return &JobStore{
		emit:    emit,
		pathFn:  jobLogPath,
		now:     time.Now,
		maxLine: 16 << 20,
	}
}

func jobLogPath() (string, error) {
	root, err := config.RootDir()
	if err != nil {
		return "", fmt.Errorf("finding root directory: %w", err)
	}
	return filepath.Join(root, specDirName, jobLogName), nil
}

func (s *JobStore) Save(ctx context.Context, job AgentJob) (*AgentJobResult, error) {
	if err := validateAgentJob(job); err != nil {
		return nil, err
	}
	path, err := s.pathFn()
	if err != nil {
		return nil, err
	}
	entry := jobEntry{
		Type:    "job_saved",
		SavedAt: s.now().UTC().Format(time.RFC3339Nano),
		Job:     job,
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("marshal agent job entry: %w", err)
	}
	line = append(line, '\n')

	s.mu.Lock()
	err = func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("create agent job directory: %w", err)
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return fmt.Errorf("open agent job log: %w", err)
		}
		defer file.Close()
		if _, err := file.Write(line); err != nil {
			return fmt.Errorf("write agent job log: %w", err)
		}
		return nil
	}()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if s.emit != nil {
		s.emit("agent.job.changed", map[string]string{
			"job_id":        job.JobID,
			"work_state":    job.WorkState,
			"payment_state": job.PaymentState,
		})
	}
	return &AgentJobResult{Job: job}, nil
}

func (s *JobStore) Get(_ context.Context, jobID string) (*AgentJobResult, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return nil, fmt.Errorf("job_id is required")
	}
	jobs, err := s.listLatest()
	if err != nil {
		return nil, err
	}
	for _, job := range jobs {
		if job.JobID == jobID {
			return &AgentJobResult{Job: job}, nil
		}
	}
	return nil, fmt.Errorf("agent job %q not found", jobID)
}

func (s *JobStore) FindByIdempotency(_ context.Context, buyer, seller, tool, key string) (*AgentJobResult, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, false, nil
	}
	jobs, err := s.listLatest()
	if err != nil {
		return nil, false, err
	}
	for _, job := range jobs {
		if job.Buyer == buyer && job.Seller == seller && job.Tool == tool && job.IdempotencyKey == key {
			return &AgentJobResult{Job: job}, true, nil
		}
	}
	return nil, false, nil
}

func (s *JobStore) List(_ context.Context, params AgentJobListParams, localBuyer string) (*AgentJobListResult, error) {
	limit := normalizeJobLimit(params.Limit)
	jobs, err := s.listLatest()
	if err != nil {
		return nil, err
	}
	result := make([]AgentJob, 0, min(limit, len(jobs)))
	for _, job := range jobs {
		if !agentJobMatchesListParams(job, params, localBuyer) {
			continue
		}
		result = append(result, job)
		if len(result) >= limit {
			break
		}
	}
	return &AgentJobListResult{Jobs: result, Count: len(result)}, nil
}

func (s *JobStore) Cancel(ctx context.Context, jobID, reason string) (*AgentJobResult, error) {
	result, err := s.Get(ctx, jobID)
	if err != nil {
		return nil, err
	}
	job := result.Job
	if isTerminalWorkState(job.WorkState) {
		return nil, fmt.Errorf("agent job %q is already %s", job.JobID, job.WorkState)
	}
	job.WorkState = JobWorkCanceled
	job.CancelReason = strings.TrimSpace(reason)
	job.UpdatedAt = s.now().UTC().Format(time.RFC3339Nano)
	return s.Save(ctx, job)
}

func (s *JobStore) listLatest() ([]AgentJob, error) {
	path, err := s.pathFn()
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []AgentJob{}, nil
		}
		return nil, fmt.Errorf("open agent job log: %w", err)
	}
	defer file.Close()

	byID := map[string]AgentJob{}
	order := []string{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), s.maxLine)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry jobEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Type != "job_saved" || validateAgentJob(entry.Job) != nil {
			continue
		}
		id := entry.Job.JobID
		if _, exists := byID[id]; exists {
			order = slices.DeleteFunc(order, func(value string) bool {
				return value == id
			})
		}
		byID[id] = entry.Job
		order = append(order, id)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read agent job log: %w", err)
	}

	jobs := make([]AgentJob, 0, len(order))
	for i := len(order) - 1; i >= 0; i-- {
		jobs = append(jobs, byID[order[i]])
	}
	return jobs, nil
}

func validateAgentJob(job AgentJob) error {
	if strings.TrimSpace(job.JobID) == "" {
		return fmt.Errorf("job.job_id is required")
	}
	if strings.TrimSpace(job.Buyer) == "" {
		return fmt.Errorf("job.buyer is required")
	}
	if strings.TrimSpace(job.Seller) == "" {
		return fmt.Errorf("job.seller is required")
	}
	if strings.TrimSpace(job.Tool) == "" {
		return fmt.Errorf("job.tool is required")
	}
	if !isKnownWorkState(job.WorkState) {
		return fmt.Errorf("job.work_state is invalid")
	}
	if !isKnownPaymentState(job.PaymentState) {
		return fmt.Errorf("job.payment_state is invalid")
	}
	if _, err := time.Parse(time.RFC3339Nano, job.CreatedAt); err != nil {
		return fmt.Errorf("job.created_at must be RFC3339: %w", err)
	}
	if _, err := time.Parse(time.RFC3339Nano, job.UpdatedAt); err != nil {
		return fmt.Errorf("job.updated_at must be RFC3339: %w", err)
	}
	return nil
}

func isKnownWorkState(state string) bool {
	switch state {
	case JobWorkReceived, JobWorkAccepted, JobWorkQueued, JobWorkRunning, JobWorkInputRequired, JobWorkCompleted, JobWorkFailed, JobWorkCanceled, JobWorkExpired:
		return true
	default:
		return false
	}
}

func isKnownPaymentState(state string) bool {
	switch state {
	case JobPaymentNone, JobPaymentRequired, JobPaymentAuthorized, JobPaymentSettled, JobPaymentFailed, JobPaymentRefunded:
		return true
	default:
		return false
	}
}

func isTerminalWorkState(state string) bool {
	switch state {
	case JobWorkCompleted, JobWorkFailed, JobWorkCanceled, JobWorkExpired:
		return true
	default:
		return false
	}
}

func agentJobMatchesListParams(job AgentJob, params AgentJobListParams, localBuyer string) bool {
	role := strings.TrimSpace(params.Role)
	switch role {
	case "":
	case "buyer":
		if job.Buyer != localBuyer {
			return false
		}
	case "seller":
		if job.Seller == localBuyer {
			return false
		}
	default:
		return false
	}
	if workState := strings.TrimSpace(params.WorkState); workState != "" && job.WorkState != workState {
		return false
	}
	if paymentState := strings.TrimSpace(params.PaymentState); paymentState != "" && job.PaymentState != paymentState {
		return false
	}
	if tool := strings.TrimSpace(params.Tool); tool != "" && job.Tool != tool && job.Capability != tool {
		return false
	}
	if agent := strings.TrimSpace(params.Agent); agent != "" && job.AgentID != agent && job.AgentName != agent && job.Seller != agent {
		return false
	}
	return true
}

func normalizeJobLimit(limit int) int {
	if limit <= 0 {
		return defaultJobLimit
	}
	if limit > maxJobLimit {
		return maxJobLimit
	}
	return limit
}

func digestJSON(raw json.RawMessage) (string, error) {
	source := strings.TrimSpace(string(raw))
	if source == "" || source == "null" {
		source = "{}"
	}
	var value any
	if err := json.Unmarshal([]byte(source), &value); err != nil {
		return "", fmt.Errorf("input must be valid JSON: %w", err)
	}
	normalized, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("normalize input JSON: %w", err)
	}
	sum := sha256.Sum256(normalized)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
