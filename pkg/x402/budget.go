package x402

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"
)

// ErrBudgetExceeded indicates a Call would exceed one of the
// configured caps (per-call, per-service-daily, or daily-total).
var ErrBudgetExceeded = errors.New("x402: budget exceeded")

// ErrPriceQuoteTooHigh indicates the server-quoted price for a Call
// exceeds the caller-supplied max_price_usdc. Returned to the agent
// so its routing rubric can fall back to a free local tool.
var ErrPriceQuoteTooHigh = errors.New("x402: price quote exceeds caller max")

// usdcDecimals is the number of decimal places USDC supports. The
// budget package internally tracks micro-USDC (10^-6) as *big.Int so
// arithmetic is exact and the wire-format strings round-trip.
const usdcDecimals = 6

// BudgetConfig is the per-agent budget policy. Strings are decimal
// USDC (e.g. "0.10"); the budget package parses them once at setup.
type BudgetConfig struct {
	PerCallMaxUSDC string
	DailyCapUSDC   string
	PerService     map[string]string
}

// Receipt is the audited record of one settled call. Mirrors
// PaymentReceipt with an added agent attribution and call context for
// the receipt log.
type Receipt struct {
	Ts           time.Time `json:"ts"`
	AgentID      string    `json:"agent_id"`
	ServiceID    string    `json:"service_id"`
	Path         string    `json:"path"`
	Tx           string    `json:"tx,omitempty"`
	Network      Network   `json:"network,omitempty"`
	AmountUSDC   string    `json:"amount_usdc"`
	MaxPriceUSDC string    `json:"max_price_usdc,omitempty"`
}

// Budget tracks per-agent caps, today's spend, and the receipt log.
// Daily counters reset at the first call of a new day in the agent's
// configured timezone (UTC for now). Receipts persist to a
// ReceiptStore when one is configured so they survive a daemon
// restart.
type Budget struct {
	mu          sync.Mutex
	agentConfig map[string]parsedBudget
	spentToday  map[string]*spendDay
	receipts    []Receipt
	store       ReceiptStore
	clock       func() time.Time
}

type parsedBudget struct {
	perCallMax *big.Int
	dailyCap   *big.Int
	perService map[string]*big.Int
}

type spendDay struct {
	day   string
	total *big.Int
	per   map[string]*big.Int
}

// NewBudget constructs an empty budget. Agents are configured via
// SetAgentBudget; receipts accumulate via Charge.
//
// store is optional: when non-nil, NewBudget loads the existing
// receipt log from the store and appends every subsequent Charge.
// Pass nil for tests that don't care about persistence.
func NewBudget(now func() time.Time, store ReceiptStore) *Budget {
	if now == nil {
		now = time.Now
	}
	b := &Budget{
		agentConfig: make(map[string]parsedBudget),
		spentToday:  make(map[string]*spendDay),
		store:       store,
		clock:       now,
	}
	if store != nil {
		if loaded, err := store.Load(); err == nil {
			b.receipts = loaded
		}
	}
	return b
}

// SetAgentBudget records or replaces the per-agent configuration.
// Returns an error if any configured amount cannot be parsed.
func (b *Budget) SetAgentBudget(agentID string, cfg BudgetConfig) error {
	parsed, err := parseBudget(cfg)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.agentConfig[agentID] = parsed
	return nil
}

// Snapshot returns the agent's current spend state for surfacing to
// the agent via x402.budget_status.
type Snapshot struct {
	PerCallMaxUSDC string
	DailyCapUSDC   string
	SpentTodayUSDC string
	PerService     []ServiceSpend
}

// ServiceSpend is one entry in Snapshot.PerService.
type ServiceSpend struct {
	ServiceID      string
	DailyCapUSDC   string
	SpentTodayUSDC string
}

// Status returns the snapshot for an agent. An unknown agent gets a
// zero snapshot; callers can decide whether to treat that as "no
// budget configured" or as "agent not enrolled."
func (b *Budget) Status(agentID string) Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	cfg, ok := b.agentConfig[agentID]
	if !ok {
		return Snapshot{}
	}
	day := b.dayLocked(agentID)
	out := Snapshot{
		PerCallMaxUSDC: formatUSDC(cfg.perCallMax),
		DailyCapUSDC:   formatUSDC(cfg.dailyCap),
		SpentTodayUSDC: formatUSDC(day.total),
	}
	for serviceID, cap := range cfg.perService {
		spent := day.per[serviceID]
		if spent == nil {
			spent = big.NewInt(0)
		}
		out.PerService = append(out.PerService, ServiceSpend{
			ServiceID:      serviceID,
			DailyCapUSDC:   formatUSDC(cap),
			SpentTodayUSDC: formatUSDC(spent),
		})
	}
	return out
}

// Authorize checks whether a call at the supplied quote would fit
// within all configured caps for the agent. Does not record any
// spend; Charge is the recording side.
//
// agentMaxPrice is the caller-supplied max_price_usdc on the
// envelope. If the server-quoted price exceeds it, returns
// ErrPriceQuoteTooHigh.
func (b *Budget) Authorize(agentID, serviceID, agentMaxPrice, serverQuote string) error {
	max, err := parseUSDC(agentMaxPrice)
	if err != nil {
		return fmt.Errorf("invalid max_price_usdc: %w", err)
	}
	quote, err := parseUSDC(serverQuote)
	if err != nil {
		return fmt.Errorf("invalid server quote: %w", err)
	}
	if quote.Cmp(max) > 0 {
		return ErrPriceQuoteTooHigh
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	cfg, ok := b.agentConfig[agentID]
	if !ok {
		return fmt.Errorf("%w: agent %q has no budget configured", ErrBudgetExceeded, agentID)
	}
	if cfg.perCallMax != nil && quote.Cmp(cfg.perCallMax) > 0 {
		return fmt.Errorf("%w: per-call cap", ErrBudgetExceeded)
	}
	day := b.dayLocked(agentID)
	if cfg.dailyCap != nil {
		projected := new(big.Int).Add(day.total, quote)
		if projected.Cmp(cfg.dailyCap) > 0 {
			return fmt.Errorf("%w: daily cap", ErrBudgetExceeded)
		}
	}
	if cap, ok := cfg.perService[serviceID]; ok {
		spent := day.per[serviceID]
		if spent == nil {
			spent = big.NewInt(0)
		}
		projected := new(big.Int).Add(spent, quote)
		if projected.Cmp(cap) > 0 {
			return fmt.Errorf("%w: per-service daily cap", ErrBudgetExceeded)
		}
	}
	return nil
}

// Charge records a settled spend and appends a Receipt. Call after a
// successful upstream call; Authorize should have run first.
func (b *Budget) Charge(receipt Receipt) error {
	amount, err := parseUSDC(receipt.AmountUSDC)
	if err != nil {
		return fmt.Errorf("invalid receipt amount: %w", err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	day := b.dayLocked(receipt.AgentID)
	day.total = new(big.Int).Add(day.total, amount)
	if day.per[receipt.ServiceID] == nil {
		day.per[receipt.ServiceID] = big.NewInt(0)
	}
	day.per[receipt.ServiceID] = new(big.Int).Add(day.per[receipt.ServiceID], amount)
	if receipt.Ts.IsZero() {
		receipt.Ts = b.clock().UTC()
	}
	b.receipts = append(b.receipts, receipt)
	if b.store != nil {
		// Persistence is best-effort: a failed append should not
		// undo an already-applied charge. The in-memory log stays
		// authoritative for the running daemon; operators see the
		// underlying error via the daemon's process-level logger
		// (the budget package itself does not own a logger).
		_ = b.store.Append(receipt)
	}
	return nil
}

// Receipts returns a copy of the in-memory receipt log filtered by
// agent. Empty slice for an agent with no charges yet.
func (b *Budget) Receipts(agentID string) []Receipt {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []Receipt
	for _, r := range b.receipts {
		if r.AgentID == agentID {
			out = append(out, r)
		}
	}
	return out
}

// AllReceipts returns a copy of every receipt across every agent
// the budget has charged, newest first. Used by the host RPC layer
// to render the Web UI's receipts panel without per-agent
// enumeration.
func (b *Budget) AllReceipts() []Receipt {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Receipt, len(b.receipts))
	copy(out, b.receipts)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// AggregateSnapshot returns a single roll-up of caps and today's
// spend across every agent the budget knows about. The Web UI uses
// this for a single-user-facing summary.
//
// Caps are reported as the most-permissive observed value (the
// user's intent is "I'm OK with up to this amount"); spend is the
// sum across agents.
type AggregateSnapshot struct {
	PerCallMaxUSDC string
	DailyCapUSDC   string
	SpentTodayUSDC string
	Agents         int
}

// AggregateStatus collapses every per-agent budget the store knows
// about into a single roll-up.
func (b *Budget) AggregateStatus() AggregateSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := AggregateSnapshot{Agents: len(b.agentConfig)}
	day := b.clock().UTC().Format("2006-01-02")
	var spentTotal, perCallMax, dailyCap *bigInt
	for agentID, cfg := range b.agentConfig {
		if cfg.perCallMax != nil {
			if perCallMax == nil || cfg.perCallMax.Cmp(perCallMax) > 0 {
				perCallMax = cfg.perCallMax
			}
		}
		if cfg.dailyCap != nil {
			if dailyCap == nil || cfg.dailyCap.Cmp(dailyCap) > 0 {
				dailyCap = cfg.dailyCap
			}
		}
		if d, ok := b.spentToday[agentID]; ok && d.day == day {
			if spentTotal == nil {
				spentTotal = new(bigInt).Set(d.total)
			} else {
				spentTotal.Add(spentTotal, d.total)
			}
		}
	}
	out.PerCallMaxUSDC = formatUSDC(perCallMax)
	out.DailyCapUSDC = formatUSDC(dailyCap)
	out.SpentTodayUSDC = formatUSDC(spentTotal)
	return out
}

// bigInt is a thin alias so AggregateStatus can use `new(bigInt)`
// without importing math/big again at the call site.
type bigInt = big.Int

// dayLocked returns the spendDay for the agent, rolling to a new day
// (zeroed counters) when the calendar day changes.
func (b *Budget) dayLocked(agentID string) *spendDay {
	day := b.clock().UTC().Format("2006-01-02")
	d, ok := b.spentToday[agentID]
	if !ok || d.day != day {
		d = &spendDay{
			day:   day,
			total: big.NewInt(0),
			per:   make(map[string]*big.Int),
		}
		b.spentToday[agentID] = d
	}
	return d
}

func parseBudget(cfg BudgetConfig) (parsedBudget, error) {
	out := parsedBudget{
		perService: make(map[string]*big.Int),
	}
	if v, err := parseUSDC(cfg.PerCallMaxUSDC); err != nil {
		return out, fmt.Errorf("per_call_max_usdc: %w", err)
	} else {
		out.perCallMax = v
	}
	if v, err := parseUSDC(cfg.DailyCapUSDC); err != nil {
		return out, fmt.Errorf("daily_cap_usdc: %w", err)
	} else {
		out.dailyCap = v
	}
	for serviceID, capStr := range cfg.PerService {
		v, err := parseUSDC(capStr)
		if err != nil {
			return out, fmt.Errorf("per_service[%s]: %w", serviceID, err)
		}
		out.perService[serviceID] = v
	}
	return out, nil
}

// CompareUSDC returns -1, 0, or +1 reflecting whether a is less
// than, equal to, or greater than b. Both arguments are decimal
// USDC strings. A parse failure on either side returns an error;
// callers that prefer to silently treat malformed values as equal
// can ignore the error and the returned int (which is 0 in that
// case).
func CompareUSDC(a, b string) (int, error) {
	av, err := parseUSDC(a)
	if err != nil {
		return 0, fmt.Errorf("compare a=%q: %w", a, err)
	}
	bv, err := parseUSDC(b)
	if err != nil {
		return 0, fmt.Errorf("compare b=%q: %w", b, err)
	}
	return av.Cmp(bv), nil
}

// parseUSDC converts a decimal USDC string ("0.005") to micro-USDC
// (5000). Empty strings are an error; the budget configuration
// requires concrete values.
func parseUSDC(s string) (*big.Int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("empty amount")
	}
	parts := strings.SplitN(s, ".", 2)
	whole := strings.TrimSpace(parts[0])
	frac := ""
	if len(parts) == 2 {
		frac = strings.TrimSpace(parts[1])
	}
	if whole == "" {
		whole = "0"
	}
	if !isAllDigits(whole) {
		return nil, fmt.Errorf("invalid whole part %q", whole)
	}
	if frac != "" && !isAllDigits(frac) {
		return nil, fmt.Errorf("invalid fractional part %q", frac)
	}
	if len(frac) > usdcDecimals {
		return nil, fmt.Errorf("amount %q has more than %d decimal places", s, usdcDecimals)
	}
	if len(frac) < usdcDecimals {
		frac = frac + strings.Repeat("0", usdcDecimals-len(frac))
	}
	combined := whole + frac
	combined = strings.TrimLeft(combined, "0")
	if combined == "" {
		combined = "0"
	}
	v, ok := new(big.Int).SetString(combined, 10)
	if !ok {
		return nil, fmt.Errorf("parse amount %q", s)
	}
	return v, nil
}

// formatUSDC converts micro-USDC back to a decimal string with up to
// usdcDecimals fractional digits, trimming trailing zeros after the
// decimal point.
func formatUSDC(v *big.Int) string {
	if v == nil {
		return "0"
	}
	s := v.String()
	for len(s) <= usdcDecimals {
		s = "0" + s
	}
	whole := s[:len(s)-usdcDecimals]
	frac := s[len(s)-usdcDecimals:]
	frac = strings.TrimRight(frac, "0")
	if frac == "" {
		return whole
	}
	return whole + "." + frac
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
