package runtimebundles

import (
	"encoding/json"
	"strings"
	"testing"
)

type testManifest struct {
	ID            string                      `json:"id"`
	Schema        int                         `json:"schema"`
	BundleVersion string                      `json:"bundle_version"`
	Runtime       map[string]string           `json:"runtime"`
	Components    map[string]testComponentRef `json:"components"`
}

type testComponentRef struct {
	Path   string `json:"path"`
	Target string `json:"target"`
}

func TestOpenClawManifestDeclaresRuntimeTuple(t *testing.T) {
	manifest := readTestManifest(t, OpenClawDir)
	if manifest.Schema != 2 {
		t.Fatalf("schema = %d, want 2", manifest.Schema)
	}
	if manifest.BundleVersion == "" {
		t.Fatal("bundle_version is empty")
	}
	if manifest.Runtime["sky10"] != "host" {
		t.Fatalf("runtime.sky10 = %q, want host", manifest.Runtime["sky10"])
	}
	if manifest.Runtime["openclaw"] != "2026.5.7" {
		t.Fatalf("runtime.openclaw = %q, want 2026.5.7", manifest.Runtime["openclaw"])
	}
	dockerImage := dockerImageArg(t, OpenClawDockerDir+"/Dockerfile", "SKY10_OPENCLAW_RUNTIME_IMAGE")
	if manifest.Runtime["docker_image"] != dockerImage {
		t.Fatalf("runtime.docker_image = %q, Dockerfile image = %q", manifest.Runtime["docker_image"], dockerImage)
	}

	pluginPackage := readPackageJSON(t, OpenClawSky10PluginDir+"/package.json")
	wantAdapter := pluginPackage.Name + "@" + pluginPackage.Version
	if manifest.Runtime["adapter"] != wantAdapter {
		t.Fatalf("runtime.adapter = %q, want %q", manifest.Runtime["adapter"], wantAdapter)
	}

	var pluginManifest struct {
		Version string `json:"version"`
	}
	unmarshalAsset(t, OpenClawSky10PluginDir+"/openclaw.plugin.json", &pluginManifest)
	if pluginManifest.Version != pluginPackage.Version {
		t.Fatalf("plugin manifest version = %q, package version = %q", pluginManifest.Version, pluginPackage.Version)
	}
}

func TestHermesManifestDeclaresRuntimeTuple(t *testing.T) {
	manifest := readTestManifest(t, HermesDir)
	if manifest.Schema != 2 {
		t.Fatalf("schema = %d, want 2", manifest.Schema)
	}
	if manifest.BundleVersion == "" {
		t.Fatal("bundle_version is empty")
	}
	if manifest.Runtime["sky10"] != "host" {
		t.Fatalf("runtime.sky10 = %q, want host", manifest.Runtime["sky10"])
	}
	if manifest.Runtime["hermes"] != "v2026.5.7" {
		t.Fatalf("runtime.hermes = %q, want v2026.5.7", manifest.Runtime["hermes"])
	}
	if manifest.Runtime["adapter"] != "hermes-sky10-bridge@0.1.0" {
		t.Fatalf("runtime.adapter = %q, want hermes-sky10-bridge@0.1.0", manifest.Runtime["adapter"])
	}
	dockerImage := dockerImageArg(t, HermesDockerDir+"/Dockerfile", "SKY10_HERMES_RUNTIME_IMAGE")
	if manifest.Runtime["docker_image"] != dockerImage {
		t.Fatalf("runtime.docker_image = %q, Dockerfile image = %q", manifest.Runtime["docker_image"], dockerImage)
	}
}

func readTestManifest(t *testing.T, id string) testManifest {
	t.Helper()

	var manifest testManifest
	unmarshalAsset(t, id+"/manifest.json", &manifest)
	if manifest.ID != id {
		t.Fatalf("id = %q, want %q", manifest.ID, id)
	}
	if len(manifest.Runtime) == 0 {
		t.Fatal("runtime tuple is empty")
	}
	if len(manifest.Components) == 0 {
		t.Fatal("components are empty")
	}
	return manifest
}

func readPackageJSON(t *testing.T, name string) struct {
	Name    string `json:"name"`
	Version string `json:"version"`
} {
	t.Helper()

	var pkg struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	unmarshalAsset(t, name, &pkg)
	if pkg.Name == "" || pkg.Version == "" {
		t.Fatalf("%s missing name/version: %#v", name, pkg)
	}
	return pkg
}

func unmarshalAsset(t *testing.T, name string, out interface{}) {
	t.Helper()

	body, err := ReadAsset(name)
	if err != nil {
		t.Fatalf("ReadAsset(%q) error: %v", name, err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("unmarshal %q: %v", name, err)
	}
}

func dockerImageArg(t *testing.T, name, arg string) string {
	t.Helper()

	body, err := ReadAsset(name)
	if err != nil {
		t.Fatalf("ReadAsset(%q) error: %v", name, err)
	}
	prefix := "ARG " + arg + "="
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			if value == "" {
				t.Fatalf("%s has empty %s", name, arg)
			}
			return value
		}
	}
	t.Fatalf("%s missing %s", name, arg)
	return ""
}
