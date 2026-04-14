package join

import (
	"fmt"
	"strings"

	skyid "github.com/sky10/sky10/pkg/id"
)

// NormalizeJoinDeviceRole validates the requested join role and returns the
// canonical manifest value. Trusted is stored as the zero/default role.
func NormalizeJoinDeviceRole(role string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "", skyid.DeviceRoleTrusted:
		return "", nil
	case skyid.DeviceRoleSandbox:
		return skyid.DeviceRoleSandbox, nil
	default:
		return "", fmt.Errorf("unsupported device role %q (supported: trusted, sandbox)", strings.TrimSpace(role))
	}
}
