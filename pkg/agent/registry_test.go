package agent

import (
	"sync"
	"testing"
)

func newTestRegistry() *Registry {
	return NewRegistry("D-testdev1", "test-host", nil)
}

func TestRegistryRegisterAndList(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()

	info, err := r.Register(RegisterParams{
		Name:         "coder",
		Endpoint:     "http://localhost:8200/rpc",
		Capabilities: []string{"code", "test"},
	}, "A-abc12345")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if info.ID != "A-abc12345" {
		t.Errorf("ID = %s, want A-abc12345", info.ID)
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

func TestRegistryDuplicateName(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()

	_, err := r.Register(RegisterParams{Name: "coder", Endpoint: "http://localhost:8200/rpc"}, "A-first000")
	if err != nil {
		t.Fatalf("first Register: %v", err)
	}

	_, err = r.Register(RegisterParams{Name: "coder", Endpoint: "http://localhost:8201/rpc"}, "A-second00")
	if err != ErrDuplicateName {
		t.Errorf("second Register error = %v, want ErrDuplicateName", err)
	}
}

func TestRegistryDeregister(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()

	r.Register(RegisterParams{Name: "coder", Endpoint: "http://localhost:8200/rpc"}, "A-abc12345")
	r.Deregister("A-abc12345")

	if r.Len() != 0 {
		t.Errorf("Len() = %d after deregister, want 0", r.Len())
	}
	if r.Get("A-abc12345") != nil {
		t.Error("Get returned non-nil after deregister")
	}
	if r.GetByName("coder") != nil {
		t.Error("GetByName returned non-nil after deregister")
	}
}

func TestRegistryReregisterAfterDeregister(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()

	r.Register(RegisterParams{Name: "coder", Endpoint: "http://localhost:8200/rpc"}, "A-first000")
	r.Deregister("A-first000")

	// Should succeed — name is free again.
	_, err := r.Register(RegisterParams{Name: "coder", Endpoint: "http://localhost:8201/rpc"}, "A-second00")
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

	r.Register(RegisterParams{Name: "coder", Endpoint: "http://localhost:8200/rpc"}, "A-abc12345")

	info := r.GetByName("coder")
	if info == nil {
		t.Fatal("GetByName returned nil")
	}
	if info.ID != "A-abc12345" {
		t.Errorf("ID = %s, want A-abc12345", info.ID)
	}

	if r.GetByName("missing") != nil {
		t.Error("GetByName(missing) returned non-nil")
	}
}

func TestRegistryResolve(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()

	r.Register(RegisterParams{Name: "coder", Endpoint: "http://localhost:8200/rpc"}, "A-abc12345")

	// Resolve by ID.
	if info := r.Resolve("A-abc12345"); info == nil || info.Name != "coder" {
		t.Error("Resolve by ID failed")
	}
	// Resolve by name.
	if info := r.Resolve("coder"); info == nil || info.ID != "A-abc12345" {
		t.Error("Resolve by name failed")
	}
	// Resolve missing.
	if r.Resolve("missing") != nil {
		t.Error("Resolve(missing) returned non-nil")
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
			r.Register(RegisterParams{Name: name, Endpoint: "http://localhost:8200/rpc"}, id)
			r.List()
			r.Resolve(name)
			r.Deregister(id)
		}(i)
	}
	wg.Wait()

	if r.Len() != 0 {
		t.Errorf("Len() = %d after concurrent register/deregister, want 0", r.Len())
	}
}
