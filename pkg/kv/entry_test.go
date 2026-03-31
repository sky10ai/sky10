package kv

import "testing"

func TestClockTupleBeats(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a, b clockTuple
		want bool
	}{
		{"higher ts wins", clockTuple{10, "a", 1}, clockTuple{5, "a", 1}, true},
		{"lower ts loses", clockTuple{5, "a", 1}, clockTuple{10, "a", 1}, false},
		{"same ts higher device wins", clockTuple{10, "b", 1}, clockTuple{10, "a", 1}, true},
		{"same ts lower device loses", clockTuple{10, "a", 1}, clockTuple{10, "b", 1}, false},
		{"same ts+device higher seq wins", clockTuple{10, "a", 2}, clockTuple{10, "a", 1}, true},
		{"same ts+device lower seq loses", clockTuple{10, "a", 1}, clockTuple{10, "a", 2}, false},
		{"equal clocks", clockTuple{10, "a", 1}, clockTuple{10, "a", 1}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.beats(tt.b); got != tt.want {
				t.Errorf("(%v).beats(%v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestExportedClockBeats(t *testing.T) {
	t.Parallel()
	a := Clock{Ts: 10, Device: "dev1", Seq: 1}
	b := Clock{Ts: 5, Device: "dev1", Seq: 1}
	if !a.Beats(b) {
		t.Error("expected a.Beats(b)")
	}
	if b.Beats(a) {
		t.Error("expected !b.Beats(a)")
	}
}

func TestClockOf(t *testing.T) {
	t.Parallel()
	vi := ValueInfo{Device: "dev1", Seq: 3}
	c := ClockOf(vi)
	if c.Device != "dev1" || c.Seq != 3 {
		t.Errorf("ClockOf = %+v, want Device=dev1 Seq=3", c)
	}
}
