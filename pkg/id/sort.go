package id

import (
	"bytes"
	"sort"
)

// sortedDeviceEntries keeps device lists deterministic for UI consumers while
// preserving the convention that the current device appears first.
func sortedDeviceEntries(devices []DeviceEntry, currentPub []byte) []DeviceEntry {
	out := append([]DeviceEntry(nil), devices...)
	sort.Slice(out, func(i, j int) bool {
		currentI := len(currentPub) > 0 && bytes.Equal(out[i].PublicKey, currentPub)
		currentJ := len(currentPub) > 0 && bytes.Equal(out[j].PublicKey, currentPub)
		if currentI != currentJ {
			return currentI
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return bytes.Compare(out[i].PublicKey, out[j].PublicKey) < 0
	})
	return out
}
