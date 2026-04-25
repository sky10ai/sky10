package sandbox

import (
	"embed"
	"fmt"

	runtimebundles "github.com/sky10/sky10/external/runtimebundles"
)

//go:embed templates/*
var bundledTemplateAssets embed.FS

func readBundledTemplateAsset(name string) ([]byte, error) {
	body, err := bundledTemplateAssets.ReadFile("templates/" + name)
	if err != nil {
		return nil, fmt.Errorf("reading bundled sandbox template %q: %w", name, err)
	}
	return body, nil
}

func readBundledRuntimeBundleAsset(name string) ([]byte, error) {
	return runtimebundles.ReadAsset(name)
}
