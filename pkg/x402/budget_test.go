package x402

import (
	"errors"
	"testing"
	"time"
)

func TestParseUSDCRoundTrip(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"0", "0"},
		{"0.000001", "0.000001"},
		{"0.005", "0.005"},
		{"1", "1"},
		{"5.00", "5"},
		{"5.50", "5.5"},
		{"100", "100"},
	}
	for _, tc := range cases {
		v, err := parseUSDC(tc.in)
		if err != nil {
			t.Fatalf("parseUSDC(%q) err = %v", tc.in, err)
		}
		if got := formatUSDC(v); got != tc.want {
			t.Fatalf("round trip %q -> %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseUSDCRejectsBadInput(t *testing.T) {
	t.Parallel()
	bad := []string{"", "abc", "1.0000001", "-1", "1.2.3", "1e6"}
	for _, in := range bad {
		if _, err := parseUSDC(in); err == nil {
			t.Errorf("parseUSDC(%q) should have failed", in)
		}
	}
}

func TestBudgetAuthorizeWithinCaps(t *testing.T) {
	t.Parallel()
	clock := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	b := NewBudget(func() time.Time { return clock }, nil)
	if err := b.SetAgentBudget("A-1", BudgetConfig{
		PerCallMaxUSDC: "0.10",
		DailyCapUSDC:   "5.00",
	}); err != nil {
		t.Fatalf("SetAgentBudget: %v", err)
	}
	if err := b.Authorize("A-1", "perplexity", "0.10", "0.005"); err != nil {
		t.Fatalf("Authorize: %v", err)
	}
}

func TestBudgetAuthorizeRejectsAboveCallerMax(t *testing.T) {
	t.Parallel()
	b := NewBudget(nil, nil)
	_ = b.SetAgentBudget("A-1", BudgetConfig{PerCallMaxUSDC: "0.10", DailyCapUSDC: "5.00"})
	err := b.Authorize("A-1", "svc", "0.001", "0.005")
	if !errors.Is(err, ErrPriceQuoteTooHigh) {
		t.Fatalf("err = %v, want ErrPriceQuoteTooHigh", err)
	}
}

func TestBudgetAuthorizeRejectsAbovePerCallCap(t *testing.T) {
	t.Parallel()
	b := NewBudget(nil, nil)
	_ = b.SetAgentBudget("A-1", BudgetConfig{PerCallMaxUSDC: "0.001", DailyCapUSDC: "5.00"})
	err := b.Authorize("A-1", "svc", "0.10", "0.005")
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("err = %v, want ErrBudgetExceeded", err)
	}
}

func TestBudgetAuthorizeRejectsAboveDailyCap(t *testing.T) {
	t.Parallel()
	b := NewBudget(nil, nil)
	_ = b.SetAgentBudget("A-1", BudgetConfig{PerCallMaxUSDC: "0.10", DailyCapUSDC: "0.01"})
	_ = b.Charge(Receipt{AgentID: "A-1", ServiceID: "svc", AmountUSDC: "0.009"})
	err := b.Authorize("A-1", "svc", "0.10", "0.005")
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("err = %v, want ErrBudgetExceeded (daily cap)", err)
	}
}

func TestBudgetAuthorizeRejectsUnknownAgent(t *testing.T) {
	t.Parallel()
	b := NewBudget(nil, nil)
	err := b.Authorize("A-unknown", "svc", "0.10", "0.005")
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("err = %v, want ErrBudgetExceeded for unconfigured agent", err)
	}
}

func TestBudgetChargeUpdatesStatus(t *testing.T) {
	t.Parallel()
	clock := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	b := NewBudget(func() time.Time { return clock }, nil)
	_ = b.SetAgentBudget("A-1", BudgetConfig{
		PerCallMaxUSDC: "0.10",
		DailyCapUSDC:   "5.00",
		PerService: map[string]string{
			"perplexity": "1.00",
		},
	})
	_ = b.Charge(Receipt{AgentID: "A-1", ServiceID: "perplexity", AmountUSDC: "0.005"})
	_ = b.Charge(Receipt{AgentID: "A-1", ServiceID: "perplexity", AmountUSDC: "0.003"})
	snap := b.Status("A-1")
	if snap.SpentTodayUSDC != "0.008" {
		t.Fatalf("spent today = %q, want 0.008", snap.SpentTodayUSDC)
	}
	if len(snap.PerService) != 1 || snap.PerService[0].SpentTodayUSDC != "0.008" {
		t.Fatalf("per-service spent = %+v", snap.PerService)
	}
	receipts := b.Receipts("A-1")
	if len(receipts) != 2 {
		t.Fatalf("receipts = %d, want 2", len(receipts))
	}
}

func TestBudgetAllReceiptsReturnsNewestFirst(t *testing.T) {
	t.Parallel()
	clock := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	now := clock
	b := NewBudget(func() time.Time { return now }, nil)
	_ = b.SetAgentBudget("A-1", BudgetConfig{PerCallMaxUSDC: "0.10", DailyCapUSDC: "5.00"})
	_ = b.SetAgentBudget("A-2", BudgetConfig{PerCallMaxUSDC: "0.10", DailyCapUSDC: "5.00"})

	_ = b.Charge(Receipt{AgentID: "A-1", ServiceID: "x", AmountUSDC: "0.001"})
	now = now.Add(time.Second)
	_ = b.Charge(Receipt{AgentID: "A-2", ServiceID: "y", AmountUSDC: "0.002"})
	now = now.Add(time.Second)
	_ = b.Charge(Receipt{AgentID: "A-1", ServiceID: "z", AmountUSDC: "0.003"})

	all := b.AllReceipts()
	if len(all) != 3 {
		t.Fatalf("len(all) = %d, want 3", len(all))
	}
	if all[0].ServiceID != "z" || all[2].ServiceID != "x" {
		t.Fatalf("ordering wrong; AllReceipts should be newest-first, got %v",
			[]string{all[0].ServiceID, all[1].ServiceID, all[2].ServiceID})
	}
}

func TestBudgetAggregateStatusSumsAcrossAgents(t *testing.T) {
	t.Parallel()
	clock := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	b := NewBudget(func() time.Time { return clock }, nil)
	_ = b.SetAgentBudget("A-1", BudgetConfig{PerCallMaxUSDC: "0.10", DailyCapUSDC: "5.00"})
	_ = b.SetAgentBudget("A-2", BudgetConfig{PerCallMaxUSDC: "0.20", DailyCapUSDC: "10.00"})
	_ = b.Charge(Receipt{AgentID: "A-1", ServiceID: "x", AmountUSDC: "0.250"})
	_ = b.Charge(Receipt{AgentID: "A-2", ServiceID: "y", AmountUSDC: "1.000"})

	snap := b.AggregateStatus()
	if snap.Agents != 2 {
		t.Fatalf("Agents = %d, want 2", snap.Agents)
	}
	if snap.PerCallMaxUSDC != "0.2" {
		t.Fatalf("PerCallMaxUSDC = %q, want 0.2 (most permissive)", snap.PerCallMaxUSDC)
	}
	if snap.DailyCapUSDC != "10" {
		t.Fatalf("DailyCapUSDC = %q, want 10", snap.DailyCapUSDC)
	}
	if snap.SpentTodayUSDC != "1.25" {
		t.Fatalf("SpentTodayUSDC = %q, want 1.25", snap.SpentTodayUSDC)
	}
}

func TestBudgetAggregateStatusEmpty(t *testing.T) {
	t.Parallel()
	b := NewBudget(nil, nil)
	snap := b.AggregateStatus()
	if snap.Agents != 0 {
		t.Fatalf("Agents = %d, want 0", snap.Agents)
	}
	if snap.SpentTodayUSDC != "0" {
		t.Fatalf("SpentTodayUSDC = %q, want 0", snap.SpentTodayUSDC)
	}
}

func TestBudgetDailyCounterRollsOver(t *testing.T) {
	t.Parallel()
	day1 := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	clock := day1
	b := NewBudget(func() time.Time { return clock }, nil)
	_ = b.SetAgentBudget("A-1", BudgetConfig{PerCallMaxUSDC: "0.10", DailyCapUSDC: "5.00"})
	_ = b.Charge(Receipt{AgentID: "A-1", ServiceID: "svc", AmountUSDC: "1.00"})
	if got := b.Status("A-1").SpentTodayUSDC; got != "1" {
		t.Fatalf("day-1 spent = %q, want 1", got)
	}
	clock = clock.Add(25 * time.Hour)
	if got := b.Status("A-1").SpentTodayUSDC; got != "0" {
		t.Fatalf("day-2 spent = %q, want 0 after rollover", got)
	}
}
