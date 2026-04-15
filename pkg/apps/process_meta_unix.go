//go:build !windows

package apps

import "os"

func currentProcessUID() (int, bool) {
	return os.Getuid(), true
}
