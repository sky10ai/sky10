package device

import "testing"

func TestPlatformForGOOS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		goos string
		want string
	}{
		{goos: "darwin", want: "macOS"},
		{goos: "linux", want: "Linux"},
		{goos: "windows", want: "Windows"},
		{goos: "freebsd", want: "unknown"},
	}

	for _, tt := range tests {
		if got := platformForGOOS(tt.goos); got != tt.want {
			t.Errorf("platformForGOOS(%q) = %q, want %q", tt.goos, got, tt.want)
		}
	}
}
