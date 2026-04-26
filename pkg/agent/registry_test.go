package agent

import (
	"reflect"
	"sync"
	"testing"
	"time"

	skykey "github.com/sky10/sky10/pkg/key"
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

func TestRegistryListReturnsSortedAgents(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()

	cases := []struct {
		name string
		id   string
	}{
		{name: "zebra", id: "A-zebra0000000000"},
		{name: "alpha", id: "A-alpha0000000000"},
		{name: "middle", id: "A-middle000000000"},
		{name: "bravo", id: "A-bravo0000000000"},
		{name: "delta", id: "A-delta0000000000"},
		{name: "charlie", id: "A-charlie00000000"},
	}
	for _, tc := range cases {
		if _, err := r.Register(RegisterParams{Name: tc.name}, tc.id); err != nil {
			t.Fatalf("Register(%q): %v", tc.name, err)
		}
	}

	want := []string{"alpha", "bravo", "charlie", "delta", "middle", "zebra"}
	for i := 0; i < 20; i++ {
		agents := r.List()
		got := make([]string, len(agents))
		for j, agent := range agents {
			got[j] = agent.Name
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("List() iteration %d names = %v, want %v", i, got, want)
		}
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

func TestRegistryRegisterStoresToolsAndIndexesCapabilitiesAsSkills(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()

	info, err := r.Register(RegisterParams{
		Name: "media",
		Tools: []AgentToolSpec{{
			Name:        "media.convert",
			Capability:  "media.convert",
			Description: "Convert media accent.",
		}},
	}, "A-media0000000000")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(info.Tools) != 1 || info.Tools[0].Name != "media.convert" {
		t.Fatalf("tools = %#v, want media accent tool", info.Tools)
	}
	if !info.HasSkill("media.convert") {
		t.Fatalf("HasSkill(media.convert) = false, want true")
	}
}

func TestRegistryReregisterByKeyNameUpdatesDisplayName(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()

	first, err := r.Register(RegisterParams{
		Name:    "Claude Code",
		KeyName: "claude-code",
		Skills:  []string{"code"},
	}, "A-first00000000000")
	if err != nil {
		t.Fatalf("first Register: %v", err)
	}

	second, err := r.Register(RegisterParams{
		Name:    "Claude",
		KeyName: "claude-code",
		Skills:  []string{"code", "test"},
	}, "A-second0000000000")
	if err != nil {
		t.Fatalf("second Register: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("re-register ID = %s, want %s", second.ID, first.ID)
	}
	if second.Name != "Claude" {
		t.Fatalf("updated name = %s, want Claude", second.Name)
	}
	if r.GetByName("Claude Code") != nil {
		t.Fatal("old display name should no longer resolve")
	}
	if info := r.GetByName("Claude"); info == nil || info.ID != first.ID {
		t.Fatal("new display name should resolve to existing agent")
	}
}

func TestRegistryRejectsDuplicateDisplayNameForDifferentKeyName(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()

	_, err := r.Register(RegisterParams{
		Name:    "coder",
		KeyName: "coder-a",
	}, "A-first00000000000")
	if err != nil {
		t.Fatalf("first Register: %v", err)
	}

	_, err = r.Register(RegisterParams{
		Name:    "coder",
		KeyName: "coder-b",
	}, "A-second0000000000")
	if err != ErrDuplicateName {
		t.Fatalf("Register duplicate display name err = %v, want %v", err, ErrDuplicateName)
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
			owner, err := skykey.Generate()
			if err != nil {
				t.Errorf("Generate owner key: %v", err)
				return
			}
			name := "agent-concurrent"
			id, _, err := GenerateAgentID(owner, name)
			if err != nil {
				t.Errorf("GenerateAgentID() error: %v", err)
				return
			}
			name = name + "-" + id
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
