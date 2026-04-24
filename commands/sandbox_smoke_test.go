package commands

import (
	"strings"
	"testing"
	"time"

	skyagent "github.com/sky10/sky10/pkg/agent"
)

func TestSelectSmokeAgentsDefaultsToAll(t *testing.T) {
	agents := []skyagent.AgentInfo{
		{ID: "A-one", Name: "one"},
		{ID: "A-two", Name: "two"},
	}
	selected, err := selectSmokeAgents(agents, nil)
	if err != nil {
		t.Fatalf("selectSmokeAgents: %v", err)
	}
	if len(selected) != 2 {
		t.Fatalf("selected len = %d, want 2", len(selected))
	}
}

func TestSelectSmokeAgentsMatchesNameOrID(t *testing.T) {
	agents := []skyagent.AgentInfo{
		{ID: "A-one", Name: "one"},
		{ID: "A-two", Name: "two"},
	}
	selected, err := selectSmokeAgents(agents, []string{"two", "A-one"})
	if err != nil {
		t.Fatalf("selectSmokeAgents: %v", err)
	}
	got := []string{selected[0].Name, selected[1].Name}
	want := []string{"two", "one"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("selected names = %v, want %v", got, want)
		}
	}
}

func TestSelectSmokeAgentsReportsMissingFilter(t *testing.T) {
	_, err := selectSmokeAgents([]skyagent.AgentInfo{{ID: "A-one", Name: "one"}}, []string{"missing"})
	if err == nil {
		t.Fatal("selectSmokeAgents error = nil, want error")
	}
	if !strings.Contains(err.Error(), `agent "missing" not found`) {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestFormatSmokeDuration(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want string
	}{
		{name: "zero", in: 0, want: "-"},
		{name: "micro", in: 500 * time.Microsecond, want: "<1ms"},
		{name: "millis", in: 1500 * time.Microsecond, want: "2ms"},
		{name: "seconds", in: 1249 * time.Millisecond, want: "1.2s"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatSmokeDuration(tc.in); got != tc.want {
				t.Fatalf("formatSmokeDuration(%s) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
