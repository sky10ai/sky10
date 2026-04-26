package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestJobStoreArtifactDownloadServesResultRef(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	outputDir := t.TempDir()
	artifactPath := filepath.Join(outputDir, "dubbed.txt")
	if err := os.WriteFile(artifactPath, []byte("artifact body"), 0o600); err != nil {
		t.Fatalf("WriteFile(artifact) error: %v", err)
	}
	now := time.Date(2026, 4, 26, 14, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	store := NewJobStore(nil)
	job := AgentJob{
		JobID:        "j_artifact",
		Buyer:        "sky10://buyer",
		Seller:       "sky10://seller",
		Tool:         "media.convert",
		WorkState:    JobWorkCompleted,
		PaymentState: JobPaymentNone,
		OutputDir:    outputDir,
		CreatedAt:    now,
		UpdatedAt:    now,
		ResultRefs: []AgentPayloadRef{{
			Kind:     "file",
			Key:      "dubbed.txt",
			URI:      (&url.URL{Scheme: "file", Path: artifactPath}).String(),
			MimeType: "text/plain",
			Size:     int64(len("artifact body")),
			Digest:   "sha256:test",
		}},
	}
	if _, err := store.Save(context.Background(), job); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/rpc/agent-jobs/artifact?job_id=j_artifact&key=dubbed.txt", nil)
	response := httptest.NewRecorder()
	store.HandleArtifactDownload(response, request)
	result := response.Result()
	defer result.Body.Close()
	if result.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(result.Body)
		t.Fatalf("status = %d body=%q, want 200", result.StatusCode, string(body))
	}
	body, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatalf("ReadAll(response) error: %v", err)
	}
	if string(body) != "artifact body" {
		t.Fatalf("body = %q, want artifact body", string(body))
	}
	if got := result.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain", got)
	}

	missing := httptest.NewRecorder()
	store.HandleArtifactDownload(missing, httptest.NewRequest(http.MethodGet, "/rpc/agent-jobs/artifact?job_id=j_artifact&key=other.txt", nil))
	if missing.Result().StatusCode != http.StatusNotFound {
		t.Fatalf("missing status = %d, want 404", missing.Result().StatusCode)
	}

	outsidePath := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0o600); err != nil {
		t.Fatalf("WriteFile(outside) error: %v", err)
	}
	job.ResultRefs = append(job.ResultRefs, AgentPayloadRef{
		Kind: "file",
		Key:  "outside.txt",
		URI:  (&url.URL{Scheme: "file", Path: outsidePath}).String(),
	})
	if _, err := store.Save(context.Background(), job); err != nil {
		t.Fatalf("Save(outside ref) error: %v", err)
	}
	forbidden := httptest.NewRecorder()
	store.HandleArtifactDownload(forbidden, httptest.NewRequest(http.MethodGet, "/rpc/agent-jobs/artifact?job_id=j_artifact&key=outside.txt", nil))
	if forbidden.Result().StatusCode != http.StatusForbidden {
		t.Fatalf("outside status = %d, want 403", forbidden.Result().StatusCode)
	}
}
