package agent

import (
	"strings"
	"testing"

	skykey "github.com/sky10/sky10/pkg/key"
)

func TestGenerateAgentID(t *testing.T) {
	t.Parallel()
	owner, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate owner key: %v", err)
	}
	id, key, err := GenerateAgentID(owner, "coder")
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

func TestGenerateAgentIDDeterministic(t *testing.T) {
	t.Parallel()
	owner, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate owner key: %v", err)
	}

	id1, key1, err := GenerateAgentID(owner, "Coder Agent")
	if err != nil {
		t.Fatalf("GenerateAgentID() error: %v", err)
	}
	id2, key2, err := GenerateAgentID(owner, "coder   agent")
	if err != nil {
		t.Fatalf("GenerateAgentID() error: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("deterministic ID mismatch: %s != %s", id1, id2)
	}
	if key1.Address() != key2.Address() {
		t.Fatalf("deterministic key mismatch: %s != %s", key1.Address(), key2.Address())
	}
}

func TestGenerateAgentIDDifferentOwners(t *testing.T) {
	t.Parallel()
	ownerA, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate owner A key: %v", err)
	}
	ownerB, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate owner B key: %v", err)
	}

	idA, _, err := GenerateAgentID(ownerA, "coder")
	if err != nil {
		t.Fatalf("GenerateAgentID(ownerA) error: %v", err)
	}
	idB, _, err := GenerateAgentID(ownerB, "coder")
	if err != nil {
		t.Fatalf("GenerateAgentID(ownerB) error: %v", err)
	}
	if idA == idB {
		t.Fatalf("expected different IDs for different owners, got %s", idA)
	}
}

func TestGenerateAgentIDDifferentKeyNames(t *testing.T) {
	t.Parallel()
	owner, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate owner key: %v", err)
	}

	idA, _, err := GenerateAgentID(owner, "coder")
	if err != nil {
		t.Fatalf("GenerateAgentID(coder) error: %v", err)
	}
	idB, _, err := GenerateAgentID(owner, "researcher")
	if err != nil {
		t.Fatalf("GenerateAgentID(researcher) error: %v", err)
	}
	if idA == idB {
		t.Fatalf("expected different IDs for different key names, got %s", idA)
	}
}

func TestAgentInfoHasSkill(t *testing.T) {
	t.Parallel()
	info := AgentInfo{
		Skills: []string{"code", "test"},
		Tools:  []AgentToolSpec{{Name: "media.accent.convert", Capability: "media.accent.convert"}},
	}
	if !info.HasSkill("code") {
		t.Error("HasSkill(code) = false, want true")
	}
	if !info.HasSkill("media.accent.convert") {
		t.Error("HasSkill(media.accent.convert) = false, want true for tool capability")
	}
	if info.HasSkill("missing") {
		t.Error("HasSkill(missing) = true, want false")
	}
}
