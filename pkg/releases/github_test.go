package releases

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGitHubClientLatestSetsHeadersAndParses(t *testing.T) {
	var gotUA string
	var gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.3","assets":[{"name":"sky10-linux-amd64","browser_download_url":"https://example.com/sky10-linux-amd64"}]}`))
	}))
	defer srv.Close()

	client := NewGitHubClient("sky10-test")
	release, err := client.Latest(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if gotUA != "sky10-test" {
		t.Fatalf("User-Agent = %q, want %q", gotUA, "sky10-test")
	}
	if gotAccept != githubAcceptHeader {
		t.Fatalf("Accept = %q, want %q", gotAccept, githubAcceptHeader)
	}
	if release.TagName != "v1.2.3" {
		t.Fatalf("TagName = %q, want %q", release.TagName, "v1.2.3")
	}
	if len(release.Assets) != 1 || release.Assets[0].Name != "sky10-linux-amd64" {
		t.Fatalf("Assets = %#v", release.Assets)
	}
}

func TestGitHubClientLatestReturnsStatusAndBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limit", http.StatusForbidden)
	}))
	defer srv.Close()

	client := NewGitHubClient("sky10-test")
	_, err := client.Latest(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "403 Forbidden") {
		t.Fatalf("error = %q, want status", err)
	}
	if !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("error = %q, want body snippet", err)
	}
}
