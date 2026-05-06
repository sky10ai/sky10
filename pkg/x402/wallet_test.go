package x402

import (
	"context"
	"errors"
	"testing"
)

// TestEVMSignReturnsErrWalletNotFundedWhenBalanceShort exercises the
// preflight path: when BalanceMicros reports a balance below the
// requirement, signEVMExact must short-circuit with
// ErrWalletNotFunded — no SignTypedData call, no malformed
// authorization on the wire.
func TestEVMSignReturnsErrWalletNotFundedWhenBalanceShort(t *testing.T) {
	t.Parallel()
	signTypedDataCalls := 0
	signer := &OWSSigner{
		WalletName: "agent-wallet",
		Now:        nowFromString("2026-04-28T12:00:00Z"),
		AddressForChain: func(_ context.Context, _, _ string) (string, error) {
			return "0x0000000000000000000000000000000000000abc", nil
		},
		SignTypedData: func(_ context.Context, _, _ string, _ []byte) (string, error) {
			signTypedDataCalls++
			t.Fatalf("SignTypedData should not be called when wallet is underfunded")
			return "", nil
		},
		BalanceMicros: func(_ context.Context, _, _ string) (uint64, error) {
			return 1000, nil // 0.001 USDC on hand
		},
	}
	req := sampleRequirement()
	req.AmountMicros = "5000000" // 5 USDC required

	_, err := signer.Sign(context.Background(), req)
	if !errors.Is(err, ErrWalletNotFunded) {
		t.Fatalf("err = %v, want ErrWalletNotFunded", err)
	}
	if signTypedDataCalls != 0 {
		t.Fatalf("SignTypedData fired %d times despite preflight failure", signTypedDataCalls)
	}
}

// TestEVMSignProceedsWhenBalanceCovers confirms the happy path
// is unchanged — sufficient balance allows signing to continue.
func TestEVMSignProceedsWhenBalanceCovers(t *testing.T) {
	t.Parallel()
	const expectedSig = "0xabc"
	signer := &OWSSigner{
		WalletName: "agent-wallet",
		Now:        nowFromString("2026-04-28T12:00:00Z"),
		AddressForChain: func(_ context.Context, _, _ string) (string, error) {
			return "0x0000000000000000000000000000000000000abc", nil
		},
		SignTypedData: func(_ context.Context, _, _ string, _ []byte) (string, error) {
			return expectedSig, nil
		},
		BalanceMicros: func(_ context.Context, _, _ string) (uint64, error) {
			return 10_000_000, nil // 10 USDC, plenty
		},
	}
	if _, err := signer.Sign(context.Background(), sampleRequirement()); err != nil {
		t.Fatalf("Sign: %v", err)
	}
}

// TestEVMSignBalanceProbeErrorIsBestEffort confirms that a transient
// RPC failure on the balance probe doesn't block signing — it just
// logs by virtue of returning the error and we continue. The check
// is documented as best-effort so the agent isn't held hostage by a
// flaky balance RPC when the actual settlement might still succeed.
func TestEVMSignBalanceProbeErrorIsBestEffort(t *testing.T) {
	t.Parallel()
	const expectedSig = "0xabc"
	signer := &OWSSigner{
		WalletName: "agent-wallet",
		Now:        nowFromString("2026-04-28T12:00:00Z"),
		AddressForChain: func(_ context.Context, _, _ string) (string, error) {
			return "0x0000000000000000000000000000000000000abc", nil
		},
		SignTypedData: func(_ context.Context, _, _ string, _ []byte) (string, error) {
			return expectedSig, nil
		},
		BalanceMicros: func(_ context.Context, _, _ string) (uint64, error) {
			return 0, errors.New("rpc unreachable")
		},
	}
	if _, err := signer.Sign(context.Background(), sampleRequirement()); err != nil {
		t.Fatalf("Sign should not fail when balance probe errors transiently: %v", err)
	}
}

// TestEVMSignSkipsPreflightWhenHookNil ensures backward compat for
// callers (and tests) that didn't wire BalanceMicros — preflight is
// opt-in and absent hook means "no check".
func TestEVMSignSkipsPreflightWhenHookNil(t *testing.T) {
	t.Parallel()
	signer := &OWSSigner{
		WalletName: "agent-wallet",
		Now:        nowFromString("2026-04-28T12:00:00Z"),
		AddressForChain: func(_ context.Context, _, _ string) (string, error) {
			return "0x0000000000000000000000000000000000000abc", nil
		},
		SignTypedData: func(_ context.Context, _, _ string, _ []byte) (string, error) {
			return "0xabc", nil
		},
	}
	if _, err := signer.Sign(context.Background(), sampleRequirement()); err != nil {
		t.Fatalf("Sign: %v", err)
	}
}
