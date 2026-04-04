package agent

import (
	"sync"
	"testing"
	"time"
)

func newTestRegistry() *Registry {
	return NewRegistry("D-testdev1", "test-host", nil)
}

func TestRegistryRegisterAndList(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()

	info, err := r.Register(RegisterParams{
		Name:   "coder",
		Skills: []string{"code", "test"},
	}, "A-abc1234567890123")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if info.ID != "A-abc1234567890123" {
		t.Errorf("ID = %s, want A-abc1234567890123", info.ID)
	}
	if info.DeviceID != "D-testdev1" {
		t.Errorf("DeviceID = %s, want D-testdev1", info.DeviceID)
	}
	if info.Status != "connected" {
		t.Errorf("Status = %s, want connected", info.Status)
	}

	agents := r.List()
	if len(agents) != 1 {
		t.Fatalf("List() len = %d, want 1", len(agents))
	}
	if agents[0].Name != "coder" {
		t.Errorf("Name = %s, want coder", agents[0].Name)
	}
}

func TestRegistryIdempotentReregister(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()

	first, err := r.Register(RegisterParams{Name: "coder", Skills: []string{"code"}}, "A-first00000000000")
	if err != nil {
		t.Fatalf("first Register: %v", err)
	}

	// Re-register with same name returns existing ID.
	second, err := r.Register(RegisterParams{Name: "coder", Skills: []string{"code", "test"}}, "A-second0000000000")
	if err != nil {
		t.Fatalf("second Register: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("re-register ID = %s, want %s", second.ID, first.ID)
	}
	// Skills should be updated.
	if len(second.Skills) != 2 {
		t.Errorf("skills = %v, want [code, test]", second.Skills)
	}
	// Only one agent in registry.
	if r.Len() != 1 {
		t.Errorf("Len() = %d, want 1", r.Len())
	}
}

func TestRegistryDeregister(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()

	r.Register(RegisterParams{Name: "coder"}, "A-abc1234567890123")
	r.Deregister("A-abc1234567890123")

	if r.Len() != 0 {
		t.Errorf("Len() = %d after deregister, want 0", r.Len())
	}
	if r.Get("A-abc1234567890123") != nil {
		t.Error("Get returned non-nil after deregister")
	}
	if r.GetByName("coder") != nil {
		t.Error("GetByName returned non-nil after deregister")
	}
}

func TestRegistryReregisterAfterDeregister(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()

	r.Register(RegisterParams{Name: "coder"}, "A-first00000000000")
	r.Deregister("A-first00000000000")

	_, err := r.Register(RegisterParams{Name: "coder"}, "A-second0000000000")
	if err != nil {
		t.Fatalf("re-register after deregister: %v", err)
	}
	if r.Len() != 1 {
		t.Errorf("Len() = %d, want 1", r.Len())
	}
}

func TestRegistryGetByName(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()

	r.Register(RegisterParams{Name: "coder"}, "A-abc1234567890123")

	info := r.GetByName("coder")
	if info == nil {
		t.Fatal("GetByName returned nil")
	}
	if info.ID != "A-abc1234567890123" {
		t.Errorf("ID = %s, want A-abc1234567890123", info.ID)
	}

	if r.GetByName("missing") != nil {
		t.Error("GetByName(missing) returned non-nil")
	}
}

func TestRegistryResolve(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()

	r.Register(RegisterParams{Name: "coder"}, "A-abc1234567890123")

	if info := r.Resolve("A-abc1234567890123"); info == nil || info.Name != "coder" {
		t.Error("Resolve by ID failed")
	}
	if info := r.Resolve("coder"); info == nil || info.ID != "A-abc1234567890123" {
		t.Error("Resolve by name failed")
	}
	if r.Resolve("missing") != nil {
		t.Error("Resolve(missing) returned non-nil")
	}
}

func TestRegistryHeartbeat(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()

	r.Register(RegisterParams{Name: "coder"}, "A-abc1234567890123")

	if !r.Heartbeat("A-abc1234567890123") {
		t.Error("Heartbeat for existing agent returned false")
	}
	if r.Heartbeat("A-missing000000000") {
		t.Error("Heartbeat for missing agent returned true")
	}

	last, ok := r.LastHeartbeat("A-abc1234567890123")
	if !ok {
		t.Fatal("LastHeartbeat returned false for existing agent")
	}
	if time.Since(last) > time.Second {
		t.Errorf("LastHeartbeat too old: %v", last)
	}
}

func TestRegistryConcurrent(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id, _, _ := GenerateAgentID()
			name := "agent-" + id
			r.Register(RegisterParams{Name: name}, id)
			r.List()
			r.Resolve(name)
			r.Heartbeat(id)
			r.Deregister(id)
		}(i)
	}
	wg.Wait()

	if r.Len() != 0 {
		t.Errorf("Len() = %d after concurrent register/deregister, want 0", r.Len())
	}
}
