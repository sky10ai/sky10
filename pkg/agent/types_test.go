package agent

import (
	"strings"
	"testing"
)

func TestGenerateAgentID(t *testing.T) {
	t.Parallel()
	id, key, err := GenerateAgentID()
	if err != nil {
		t.Fatalf("GenerateAgentID() error: %v", err)
	}
	if !strings.HasPrefix(id, "A-") {
		t.Errorf("agent ID %q missing A- prefix", id)
	}
	if len(id) != 18 { // "A-" + 16 chars
		t.Errorf("agent ID length = %d, want 18", len(id))
	}
	if key == nil {
		t.Fatal("key is nil")
	}
	if key.PublicKey == nil {
		t.Fatal("public key is nil")
	}
}

func TestGenerateAgentIDUnique(t *testing.T) {
	t.Parallel()
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id, _, err := GenerateAgentID()
		if err != nil {
			t.Fatalf("GenerateAgentID() error: %v", err)
		}
		if seen[id] {
			t.Fatalf("duplicate agent ID: %s", id)
		}
		seen[id] = true
	}
}

func TestAgentInfoHasMethod(t *testing.T) {
	t.Parallel()
	info := AgentInfo{
		Methods: []MethodSpec{
			{Name: "search"},
			{Name: "summarize"},
		},
	}
	if !info.HasMethod("search") {
		t.Error("HasMethod(search) = false, want true")
	}
	if info.HasMethod("missing") {
		t.Error("HasMethod(missing) = true, want false")
	}
}

func TestAgentInfoHasCapability(t *testing.T) {
	t.Parallel()
	info := AgentInfo{
		Capabilities: []string{"code", "test"},
	}
	if !info.HasCapability("code") {
		t.Error("HasCapability(code) = false, want true")
	}
	if info.HasCapability("missing") {
		t.Error("HasCapability(missing) = true, want false")
	}
}
