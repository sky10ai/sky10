package agent

import (
	"reflect"
	"testing"
)

func TestSortAgentInfosOrdersByNameDeviceAndID(t *testing.T) {
	t.Parallel()

	agents := []AgentInfo{
		{Name: "beta", DeviceID: "D-device02", ID: "A-beta0200000000"},
		{Name: "alpha", DeviceID: "D-device03", ID: "A-alpha030000000"},
		{Name: "alpha", DeviceID: "D-device01", ID: "A-alpha010000000"},
		{Name: "alpha", DeviceID: "D-device01", ID: "A-alpha010000001"},
	}

	sortAgentInfos(agents)

	got := make([]string, len(agents))
	for i, agent := range agents {
		got[i] = agent.ID
	}
	want := []string{
		"A-alpha010000000",
		"A-alpha010000001",
		"A-alpha030000000",
		"A-beta0200000000",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sortAgentInfos() IDs = %v, want %v", got, want)
	}
}
