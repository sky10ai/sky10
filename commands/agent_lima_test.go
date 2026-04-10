package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractPortSuffix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty host", in: ":9101", want: ":9101"},
		{name: "ipv4", in: "127.0.0.1:9101", want: ":9101"},
		{name: "ipv6", in: "[::]:9101", want: ":9101"},
		{name: "wildcard", in: "0.0.0.0:9101", want: ":9101"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractPortSuffix(tt.in)
			if err != nil {
				t.Fatalf("extractPortSuffix(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("extractPortSuffix(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestGuestRPCURLFromHTTPAddr(t *testing.T) {
	t.Parallel()

	got, err := guestRPCURLFromHTTPAddr("[::]:9101")
	if err != nil {
		t.Fatalf("guestRPCURLFromHTTPAddr: %v", err)
	}
	if want := "http://host.lima.internal:9101"; got != want {
		t.Fatalf("guestRPCURLFromHTTPAddr = %q, want %q", got, want)
	}
}

func TestDefaultLimaSharedDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := defaultLimaSharedDir("bobs-burgers")
	if err != nil {
		t.Fatalf("defaultLimaSharedDir: %v", err)
	}

	want := filepath.Join(home, "sky10", "sandboxes", "bobs-burgers")
	if got != want {
		t.Fatalf("defaultLimaSharedDir = %q, want %q", got, want)
	}
}

func TestSandboxHostnameAndURL(t *testing.T) {
	t.Parallel()

	if got, want := sandboxHostname("bobs-burgers"), "bobs-burgers.sb.sky10.local"; got != want {
		t.Fatalf("sandboxHostname = %q, want %q", got, want)
	}

	if got, want := sandboxHTTPSURL("bobs-burgers"), "https://bobs-burgers.sb.sky10.local:18790/chat?session=main"; got != want {
		t.Fatalf("sandboxHTTPSURL = %q, want %q", got, want)
	}
}

func TestWalkUp(t *testing.T) {
	t.Parallel()

	base := filepath.Join(string(filepath.Separator), "tmp", "sky10", "nested")
	got := walkUp(base)
	if len(got) < 4 {
		t.Fatalf("walkUp(%q) returned too few directories: %v", base, got)
	}
	if got[0] != base {
		t.Fatalf("walkUp(%q) first dir = %q, want %q", base, got[0], base)
	}
	if got[len(got)-1] != string(filepath.Separator) {
		t.Fatalf("walkUp(%q) last dir = %q, want %q", base, got[len(got)-1], string(filepath.Separator))
	}
}

func TestHasLimaTemplateAssets(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, name := range append(append([]string(nil), agentLimaAssetFiles...), agentLimaHostsScript) {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(name), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}
	}

	if !hasLimaTemplateAssets(dir) {
		t.Fatal("hasLimaTemplateAssets() = false, want true")
	}
}

func TestValidateSandboxCreate(t *testing.T) {
	t.Parallel()

	if err := validateSandboxCreate(sandboxProviderLima, sandboxTemplateOpenClaw); err != nil {
		t.Fatalf("validateSandboxCreate(valid): %v", err)
	}
	if err := validateSandboxCreate("docker", sandboxTemplateOpenClaw); err == nil {
		t.Fatal("validateSandboxCreate(docker): want error")
	}
	if err := validateSandboxCreate(sandboxProviderLima, "claude"); err == nil {
		t.Fatal("validateSandboxCreate(unknown template): want error")
	}
}

func TestRenderLimaTemplate(t *testing.T) {
	t.Parallel()

	body := []byte(`name=__SKY10_SANDBOX_NAME__ path=__SKY10_SHARED_DIR__`)
	got := string(renderLimaTemplate(body, "bobs-burgers", "/Users/bf/sky10/sandboxes/bobs-burgers"))

	if strings.Contains(got, templateNameToken) || strings.Contains(got, templateSharedToken) {
		t.Fatalf("renderLimaTemplate() left placeholder tokens behind: %q", got)
	}
	if !strings.Contains(got, "bobs-burgers") {
		t.Fatalf("renderLimaTemplate() missing sandbox name: %q", got)
	}
	if !strings.Contains(got, "/Users/bf/sky10/sandboxes/bobs-burgers") {
		t.Fatalf("renderLimaTemplate() missing shared dir: %q", got)
	}
}
