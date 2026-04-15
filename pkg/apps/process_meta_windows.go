//go:build windows

package apps

func currentProcessUID() (int, bool) {
	return 0, false
}
