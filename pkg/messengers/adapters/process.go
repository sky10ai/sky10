package adapters

import (
	"fmt"
	"path/filepath"
	"strings"

	messagingruntime "github.com/sky10/sky10/pkg/messaging/runtime"
)

// BuiltinProcessSpec returns the self-exec process spec for one built-in
// messaging adapter served by the current sky10 binary.
func BuiltinProcessSpec(executablePath, name string) (messagingruntime.ProcessSpec, error) {
	executablePath = strings.TrimSpace(executablePath)
	if executablePath == "" {
		return messagingruntime.ProcessSpec{}, fmt.Errorf("builtin adapter executable path is required")
	}
	definition, ok := Lookup(name)
	if !ok {
		return messagingruntime.ProcessSpec{}, fmt.Errorf("messaging adapter %q is not registered", name)
	}
	return messagingruntime.ProcessSpec{
		Path: filepath.Clean(executablePath),
		Args: []string{"messaging", definition.Name},
	}, nil
}
