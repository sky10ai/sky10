package id

import (
	"reflect"
	"testing"
)

func TestSortedDeviceEntriesOrdersCurrentFirstThenNameThenPubKey(t *testing.T) {
	t.Parallel()

	current := []byte{0x03}
	devices := []DeviceEntry{
		{Name: "beta", PublicKey: []byte{0x02}},
		{Name: "alpha", PublicKey: []byte{0x04}},
		{Name: "alpha", PublicKey: []byte{0x01}},
		{Name: "zeta", PublicKey: current},
	}

	sorted := sortedDeviceEntries(devices, current)

	got := make([][]byte, len(sorted))
	for i, device := range sorted {
		got[i] = device.PublicKey
	}
	want := [][]byte{
		{0x03},
		{0x01},
		{0x04},
		{0x02},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sortedDeviceEntries() pubkeys = %v, want %v", got, want)
	}
}
