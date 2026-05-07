package rootagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/config"
)

func TestStoreSaveAppendsJSONLAndListReturnsLatestPerRun(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	store := NewStore(nil)
	first := testRun("run-1", "running", "first answer")
	if _, err := store.Save(RunSaveParams{Run: first}); err != nil {
		t.Fatalf("Save(first): %v", err)
	}
	updated := first
	updated.Status = "complete"
	updated.Answer = "complete answer"
	updated.UpdatedAt = "2026-04-26T01:02:05Z"
	if _, err := store.Save(RunSaveParams{Run: updated}); err != nil {
		t.Fatalf("Save(updated): %v", err)
	}
	second := testRun("run-2", "complete", "second answer")
	second.UpdatedAt = "2026-04-26T01:02:06Z"
	if _, err := store.Save(RunSaveParams{Run: second}); err != nil {
		t.Fatalf("Save(second): %v", err)
	}

	result, err := store.List(HistoryListParams{Limit: 12})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result.Runs) != 2 {
		t.Fatalf("len(result.Runs) = %d, want 2", len(result.Runs))
	}
	if result.Runs[0].ID != "run-2" {
		t.Fatalf("first ID = %q, want run-2", result.Runs[0].ID)
	}
	if result.Runs[1].Answer != "complete answer" || result.Runs[1].Status != "complete" {
		t.Fatalf("run-1 = %#v, want latest complete snapshot", result.Runs[1])
	}

	path := filepath.Join(os.Getenv(config.EnvHome), "rootagent", "runs.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	lines := splitNonEmptyLines(string(data))
	if len(lines) != 3 {
		t.Fatalf("history line count = %d, want 3", len(lines))
	}
	var entry historyEntry
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("unmarshal history entry: %v", err)
	}
	if entry.Type != "run_saved" || entry.Run.ID != "run-1" {
		t.Fatalf("entry = %#v, want run_saved run-1", entry)
	}
}

func TestStoreListSkipsInvalidLines(t *testing.T) {
	root := t.TempDir()
	t.Setenv(config.EnvHome, root)

	path := filepath.Join(root, "rootagent", "runs.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	good := historyEntry{
		Type:    "run_saved",
		SavedAt: time.Date(2026, 4, 26, 1, 2, 3, 0, time.UTC).Format(time.RFC3339Nano),
		Run:     testRun("valid", "complete", "ok"),
	}
	goodLine, err := json.Marshal(good)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	content := append([]byte("{bad json}\n"), goodLine...)
	content = append(content, '\n')
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := NewStore(nil).List(HistoryListParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result.Runs) != 1 || result.Runs[0].ID != "valid" {
		t.Fatalf("result.Runs = %#v, want only valid run", result.Runs)
	}
}

func TestRPCHandlerDispatch(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	var emitted []string
	store := NewStore(func(event string, _ interface{}) {
		emitted = append(emitted, event)
	})
	handler := NewRPCHandler(store)

	if _, _, handled := handler.Dispatch(context.Background(), "system.health", nil); handled {
		t.Fatal("Dispatch(system.health) handled = true, want false")
	}

	saveParams, err := json.Marshal(RunSaveParams{Run: testRun("rpc-run", "complete", "via rpc")})
	if err != nil {
		t.Fatalf("Marshal save params: %v", err)
	}
	raw, err, handled := handler.Dispatch(context.Background(), "rootAgent.runSave", saveParams)
	if err != nil {
		t.Fatalf("Dispatch(rootAgent.runSave): %v", err)
	}
	if !handled {
		t.Fatal("Dispatch(rootAgent.runSave) handled = false, want true")
	}
	if raw.(*RunSaveResult).Status != "saved" {
		t.Fatalf("save result = %#v, want saved", raw)
	}
	if len(emitted) != 1 || emitted[0] != "rootAgent.history.changed" {
		t.Fatalf("emitted = %#v, want rootAgent.history.changed", emitted)
	}

	raw, err, handled = handler.Dispatch(context.Background(), "rootAgent.historyList", nil)
	if err != nil {
		t.Fatalf("Dispatch(rootAgent.historyList): %v", err)
	}
	if !handled {
		t.Fatal("Dispatch(rootAgent.historyList) handled = false, want true")
	}
	result := raw.(*HistoryListResult)
	if len(result.Runs) != 1 || result.Runs[0].ID != "rpc-run" {
		t.Fatalf("result.Runs = %#v, want rpc-run", result.Runs)
	}
}

func testRun(id, status, answer string) RunRecord {
	return RunRecord{
		ID:        id,
		Audience:  "for_me",
		Prompt:    "what is the version of sky10?",
		Answer:    answer,
		Status:    status,
		CreatedAt: "2026-04-26T01:02:03Z",
		UpdatedAt: "2026-04-26T01:02:04Z",
		ToolTraces: []ToolTrace{
			{
				ID:        "trace-1",
				Title:     "Version",
				Tool:      "readSystemHealth",
				RPCMethod: "system.health",
				Status:    "complete",
				Detail:    "Read daemon version",
				StartedAt: "2026-04-26T01:02:03Z",
			},
		},
	}
}

func splitNonEmptyLines(value string) []string {
	var lines []string
	start := 0
	for index, char := range value {
		if char != '\n' {
			continue
		}
		if line := value[start:index]; line != "" {
			lines = append(lines, line)
		}
		start = index + 1
	}
	if start < len(value) {
		lines = append(lines, value[start:])
	}
	return lines
}
