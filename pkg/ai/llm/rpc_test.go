package llm

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestRPCSaveListDeleteConnection(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "connections.json"))
	var events []string
	handler := NewRPCHandler(store, func(event string, _ interface{}) {
		events = append(events, event)
	})

	raw, _ := json.Marshal(ConnectionSaveParams{Connection: Connection{
		ID:       "venice",
		Label:    "Venice",
		Provider: ProviderVenice,
		BaseURL:  DefaultVeniceBaseURL,
		Auth:     AuthConfig{Method: AuthMethodX402},
	}})
	result, err, handled := handler.Dispatch(context.Background(), "inference.connectionSave", raw)
	if !handled {
		t.Fatal("connectionSave not handled")
	}
	if err != nil {
		t.Fatalf("connectionSave error = %v", err)
	}
	saveResult, ok := result.(ConnectionSaveResult)
	if !ok {
		t.Fatalf("result type = %T, want ConnectionSaveResult", result)
	}
	if saveResult.Connection.Auth.ServiceID != DefaultVeniceX402Service {
		t.Fatalf("service id = %q", saveResult.Connection.Auth.ServiceID)
	}

	result, err, handled = handler.Dispatch(context.Background(), "inference.connections", nil)
	if !handled {
		t.Fatal("connections not handled")
	}
	if err != nil {
		t.Fatalf("connections error = %v", err)
	}
	listResult, ok := result.(ConnectionsResult)
	if !ok {
		t.Fatalf("result type = %T, want ConnectionsResult", result)
	}
	if listResult.Count != 1 {
		t.Fatalf("count = %d, want 1", listResult.Count)
	}

	raw, _ = json.Marshal(ConnectionDeleteParams{ID: "venice"})
	if _, err, handled = handler.Dispatch(context.Background(), "inference.connectionDelete", raw); !handled || err != nil {
		t.Fatalf("connectionDelete handled=%v error=%v", handled, err)
	}
	if len(events) != 2 || events[0] != connectionsUpdatedEvent || events[1] != connectionsUpdatedEvent {
		t.Fatalf("events = %+v", events)
	}
}
