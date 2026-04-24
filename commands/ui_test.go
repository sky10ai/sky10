package commands

import "testing"

func TestBrowserCommand(t *testing.T) {
	url := "http://localhost:9101"
	tests := []struct {
		goos     string
		wantName string
		wantArgs []string
	}{
		{goos: "darwin", wantName: "open", wantArgs: []string{url}},
		{goos: "linux", wantName: "xdg-open", wantArgs: []string{url}},
		{goos: "windows", wantName: "cmd", wantArgs: []string{"/c", "start", "", url}},
	}

	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			gotName, gotArgs, err := browserCommand(tt.goos, url)
			if err != nil {
				t.Fatalf("browserCommand: %v", err)
			}
			if gotName != tt.wantName {
				t.Fatalf("name = %q, want %q", gotName, tt.wantName)
			}
			if len(gotArgs) != len(tt.wantArgs) {
				t.Fatalf("args = %#v, want %#v", gotArgs, tt.wantArgs)
			}
			for i := range gotArgs {
				if gotArgs[i] != tt.wantArgs[i] {
					t.Fatalf("args = %#v, want %#v", gotArgs, tt.wantArgs)
				}
			}
		})
	}
}

func TestBrowserCommandRejectsUnsupportedPlatform(t *testing.T) {
	if _, _, err := browserCommand("plan9", "http://localhost:9101"); err == nil {
		t.Fatal("expected unsupported platform error")
	}
}
