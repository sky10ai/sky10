package rpc

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestWebUIHandlerSetsCacheHeaders(t *testing.T) {
	original := WebDist
	WebDist = fstest.MapFS{
		"web/dist/index.html":    &fstest.MapFile{Data: []byte("<!doctype html>")},
		"web/dist/assets/app.js": &fstest.MapFile{Data: []byte("console.log('ok')")},
		"web/dist/favicon.svg":   &fstest.MapFile{Data: []byte("<svg></svg>")},
	}
	t.Cleanup(func() {
		WebDist = original
	})

	handler := webUIHandler()

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantCache  string
	}{
		{
			name:       "root serves index without cache",
			path:       "/",
			wantStatus: http.StatusOK,
			wantCache:  "no-store",
		},
		{
			name:       "spa fallback serves index without cache",
			path:       "/settings/sandboxes",
			wantStatus: http.StatusOK,
			wantCache:  "no-store",
		},
		{
			name:       "hashed assets are immutable",
			path:       "/assets/app.js",
			wantStatus: http.StatusOK,
			wantCache:  "public, max-age=31536000, immutable",
		},
		{
			name:       "other static files revalidate",
			path:       "/favicon.svg",
			wantStatus: http.StatusOK,
			wantCache:  "no-cache",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if got := rec.Header().Get("Cache-Control"); got != tc.wantCache {
				t.Fatalf("Cache-Control = %q, want %q", got, tc.wantCache)
			}
		})
	}
}

func TestWebUIHandlerWithoutAssets(t *testing.T) {
	original := WebDist
	WebDist = fstest.MapFS{}
	t.Cleanup(func() {
		WebDist = original
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	webUIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

var _ fs.FS = fstest.MapFS{}
