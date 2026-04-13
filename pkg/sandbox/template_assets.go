package sandbox

import (
	"embed"
	"fmt"
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
