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

// UninstallAuditInfo carries durable caller metadata for managed OWS removal.
type UninstallAuditInfo = skyapps.UninstallAuditInfo

// BinDir returns the directory containing stable managed app entrypoints (~/.sky10/bin/).
func BinDir() (string, error) {
	return skyapps.BinDir()
}

// BinPath returns the stable sky10-managed entrypoint path for OWS.
func BinPath() (string, error) {
	return skyapps.ManagedPath(skyapps.AppOWS)
}

// CheckRelease queries GitHub for the latest OWS release and compares
// it to the currently installed version (if any).
func CheckRelease(current string) (*ReleaseInfo, error) {
	return skyapps.CheckRelease(skyapps.AppOWS, current)
}

// Install downloads OWS into the managed app version store and activates
// the stable ~/.sky10/bin/ows entrypoint.
func Install(info *ReleaseInfo, onProgress ProgressFunc) error {
	return skyapps.Install(skyapps.AppOWS, info, onProgress)
}

// Uninstall removes the active managed OWS binary and its stable entrypoint.
func Uninstall() (*UninstallResult, error) {
	return skyapps.Uninstall(skyapps.AppOWS)
}

// UninstallWithAudit removes the active managed OWS binary and records caller metadata.
func UninstallWithAudit(info UninstallAuditInfo) (*UninstallResult, error) {
	return skyapps.UninstallWithAudit(skyapps.AppOWS, info)
}

// InstalledVersion returns the version of the active OWS binary,
// or "" if not installed.
func InstalledVersion() string {
	return skyapps.InstalledVersion(skyapps.AppOWS)
}
