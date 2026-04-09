package wallet

import skyapps "github.com/sky10/sky10/pkg/apps"

// ProgressFunc reports download progress (bytes downloaded, total bytes).
type ProgressFunc = skyapps.ProgressFunc

// Emitter sends named events to subscribers.
type Emitter func(event string, data interface{})

// ReleaseInfo holds version information for OWS.
type ReleaseInfo = skyapps.ReleaseInfo

// UninstallResult describes the outcome of removing the managed OWS binary.
type UninstallResult = skyapps.UninstallResult

// BinDir returns the directory where managed binaries live (~/.sky10/bin/).
func BinDir() (string, error) {
	return skyapps.BinDir()
}

// BinPath returns the full path to the managed ows binary.
func BinPath() (string, error) {
	return skyapps.ManagedPath(skyapps.AppOWS)
}

// CheckRelease queries GitHub for the latest OWS release and compares
// it to the currently installed version (if any).
func CheckRelease(current string) (*ReleaseInfo, error) {
	return skyapps.CheckRelease(skyapps.AppOWS, current)
}

// Install downloads the OWS binary to ~/.sky10/bin/ows.
func Install(info *ReleaseInfo, onProgress ProgressFunc) error {
	return skyapps.Install(skyapps.AppOWS, info, onProgress)
}

// Uninstall removes the managed OWS binary from sky10's bin directory.
func Uninstall() (*UninstallResult, error) {
	return skyapps.Uninstall(skyapps.AppOWS)
}

// InstalledVersion returns the version of the active OWS binary,
// or "" if not installed.
func InstalledVersion() string {
	return skyapps.InstalledVersion(skyapps.AppOWS)
}
