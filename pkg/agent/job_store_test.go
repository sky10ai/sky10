package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/config"
)

func TestJobStorePersistsLatestJobSnapshots(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	now := time.Date(2026, 4, 26, 14, 0, 0, 0, time.UTC)
	store := NewJobStore(nil)
	store.now = func() time.Time { return now }

	job := AgentJob{
		JobID:        "j_test",
		Buyer:        "sky10://buyer",
		Seller:       "sky10://seller",
		AgentID:      "A-worker",
		AgentName:    "worker",
		Tool:         "media.convert",
		Capability:   "media.convert",
		WorkState:    JobWorkAccepted,
		PaymentState: JobPaymentNone,
		CreatedAt:    now.Format(time.RFC3339Nano),
		UpdatedAt:    now.Format(time.RFC3339Nano),
		InputDigest:  "sha256:abc",
	}
	if _, err := store.Save(context.Background(), job); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	job.WorkState = JobWorkRunning
	job.UpdatedAt = now.Add(time.Minute).Format(time.RFC3339Nano)
	if _, err := store.Save(context.Background(), job); err != nil {
		t.Fatalf("Save(update) error: %v", err)
	}

	got, err := store.Get(context.Background(), "j_test")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.Job.WorkState != JobWorkRunning {
		t.Fatalf("work state = %q, want running", got.Job.WorkState)
	}
	list, err := store.List(context.Background(), AgentJobListParams{Tool: "media.convert"}, "sky10://buyer")
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if list.Count != 1 || list.Jobs[0].JobID != "j_test" {
		t.Fatalf("list = %#v, want j_test", list)
	}

	path := filepath.Join(os.Getenv(config.EnvHome), "agents", "jobs.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error: %v", path, err)
	}
	if got := len(splitNonEmptySpecLines(string(data))); got != 2 {
		t.Fatalf("job log lines = %d, want 2", got)
	}
	if strings.Contains(string(data), "private payload") {
		t.Fatalf("job log persisted private payload: %q", string(data))
	}
}

func TestDigestJSONCanonicalizesObjectInput(t *testing.T) {
	first, err := digestJSON(json.RawMessage(`{"b":2,"a":1}`))
	if err != nil {
		t.Fatalf("digestJSON(first) error: %v", err)
	}
	second, err := digestJSON(json.RawMessage(`{"a":1,"b":2}`))
	if err != nil {
		t.Fatalf("digestJSON(second) error: %v", err)
	}
	if first != second {
		t.Fatalf("digests differ: %s != %s", first, second)
	}
}
