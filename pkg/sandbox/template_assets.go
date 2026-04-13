package sandbox

import (
	"embed"
	"fmt"
)

//go:embed templates/*.yaml
var bundledTemplates embed.FS

func readBundledTemplate(name string) ([]byte, error) {
	body, err := bundledTemplates.ReadFile("templates/" + name)
	if err != nil {
		return nil, fmt.Errorf("reading bundled sandbox template %q: %w", name, err)
	}
	return body, nil
}
