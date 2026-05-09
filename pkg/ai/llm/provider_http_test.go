package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProviderDoJSONReturnsUpstreamErrors(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = fmt.Fprintf(w, `{"error":{"message":"status %d"}}`, status)
			}))
			defer srv.Close()

			var out map[string]interface{}
			err := providerDoJSON(context.Background(), srv.Client(), providerRequestOptions{
				Provider:  "test-provider",
				Operation: "test operation",
				URL:       srv.URL,
				Body:      map[string]string{"hello": "world"},
			}, &out)
			if err == nil {
				t.Fatal("providerDoJSON() error = nil, want upstream error")
			}
			if !strings.Contains(err.Error(), fmt.Sprintf("HTTP %d", status)) || !strings.Contains(err.Error(), fmt.Sprintf("status %d", status)) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestProviderDoJSONSendsHeadersAndBody(t *testing.T) {
	t.Parallel()

	var gotAuth, gotAccept, gotContentType string
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotContentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	var out map[string]interface{}
	if err := providerDoJSON(context.Background(), srv.Client(), providerRequestOptions{
		Provider:  "test-provider",
		Operation: "test operation",
		URL:       srv.URL,
		Accept:    "application/json",
		Headers:   map[string]string{"Authorization": "Bearer test"},
		Body:      map[string]string{"hello": "world"},
	}, &out); err != nil {
		t.Fatalf("providerDoJSON() error = %v", err)
	}
	if gotAuth != "Bearer test" || gotAccept != "application/json" || gotContentType != "application/json" {
		t.Fatalf("headers auth=%q accept=%q content-type=%q", gotAuth, gotAccept, gotContentType)
	}
	if gotBody["hello"] != "world" {
		t.Fatalf("body = %+v", gotBody)
	}
}

func TestProviderHTTPClientDefaultTimeout(t *testing.T) {
	t.Parallel()

	client := providerHTTPClient(nil)
	if client == nil {
		t.Fatal("providerHTTPClient(nil) = nil")
	}
	if client.Timeout != defaultProviderHTTPTimeout {
		t.Fatalf("Timeout = %v, want %v", client.Timeout, defaultProviderHTTPTimeout)
	}
	custom := &http.Client{}
	if got := providerHTTPClient(custom); got != custom {
		t.Fatal("providerHTTPClient(custom) did not preserve custom client")
	}
}

func TestOpenAIAdapterStreamMalformedSSE(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {not-json}\n\n")
	}))
	defer srv.Close()

	adapter := NewOpenAIAdapter(OpenAIAdapterOptions{
		APIKey:  "sk-test",
		BaseURL: srv.URL,
		Model:   "gpt-test",
	})
	err := adapter.StreamChatCompletions(context.Background(), ChatCompletionRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, func(ChatCompletionStreamChunk) error {
		return nil
	})
	if err == nil {
		t.Fatal("StreamChatCompletions() error = nil, want malformed SSE error")
	}
	if !strings.Contains(err.Error(), "decode openai stream chunk") {
		t.Fatalf("error = %v", err)
	}
}
