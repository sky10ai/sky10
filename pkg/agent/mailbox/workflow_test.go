package mailbox

import (
	"context"
	"encoding/json"
	"testing"
)

func TestTaskAndApprovalWorkflowIdempotentTransitions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, backend := newTestMailboxStore(t)

	task, err := store.CreateTaskRequest(ctx, Item{
		From:           Principal{ID: "agent:planner", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		To:             &Principal{ID: "queue:research", Kind: PrincipalKindCapabilityQueue, Scope: ScopePrivateNetwork},
		TargetSkill:    "research",
		RequestID:      "req-task-1",
		IdempotencyKey: "task-create-1",
	}, TaskRequestPayload{
		Method:  "research",
		Summary: "deep query",
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err = store.CompleteTaskRequest(ctx, task.Item.ID, Principal{ID: "agent:worker", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork}, "complete-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.State != StateCompleted {
		t.Fatalf("task state = %s, want %s", task.State, StateCompleted)
	}
	taskAgain, err := store.CompleteTaskRequest(ctx, task.Item.ID, Principal{ID: "agent:worker", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork}, "complete-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(taskAgain.Events) != len(task.Events) {
		t.Fatalf("duplicate completion added events: got %d want %d", len(taskAgain.Events), len(task.Events))
	}

	reloaded, err := NewStore(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := reloaded.CreateApprovalRequest(ctx, Item{
		From:           Principal{ID: "agent:provider", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		To:             &Principal{ID: "human:alice", Kind: PrincipalKindHuman, Scope: ScopePrivateNetwork},
		RequestID:      "req-approval-1",
		IdempotencyKey: "approval-create-1",
	}, ApprovalRequestPayload{
		Action:  "approve_payment",
		Summary: "Approve 2 USDC payment",
	})
	if err != nil {
		t.Fatal(err)
	}
	approval, err = reloaded.Approve(ctx, approval.Item.ID, Principal{ID: "human:alice", Kind: PrincipalKindHuman, Scope: ScopePrivateNetwork}, "decision-1")
	if err != nil {
		t.Fatal(err)
	}
	if approval.State != StateApproved {
		t.Fatalf("approval state = %s, want %s", approval.State, StateApproved)
	}
	approvalAgain, err := reloaded.Approve(ctx, approval.Item.ID, Principal{ID: "human:alice", Kind: PrincipalKindHuman, Scope: ScopePrivateNetwork}, "decision-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(approvalAgain.Events) != len(approval.Events) {
		t.Fatalf("duplicate approval added events: got %d want %d", len(approvalAgain.Events), len(approval.Events))
	}
}

func TestPaymentWorkflowReplayAndDedupeAcrossRestart(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, backend := newTestMailboxStore(t)

	required, err := store.CreatePaymentRequired(ctx, Item{
		From:           Principal{ID: "agent:provider", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		To:             &Principal{ID: "agent:caller", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		RequestID:      "req-pay-1",
		IdempotencyKey: "payment-required-1",
	}, PaymentRequiredPayload{
		Method:  "research",
		Amount:  "2000000",
		Asset:   "USDC",
		Chain:   "solana:testnet",
		Address: "7xKXtg",
		Nonce:   "nonce-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	store, err = NewStore(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := store.CreatePaymentProof(ctx, Item{
		From:           Principal{ID: "agent:caller", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		ReplyTo:        required.Item.ID,
		IdempotencyKey: "payment-proof-1",
	}, PaymentProofPayload{
		SignedTx: "signed-tx-bytes",
		Chain:    "solana:testnet",
		Amount:   "2000000",
		Nonce:    "nonce-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	proofDup, err := store.CreatePaymentProof(ctx, Item{
		From:           Principal{ID: "agent:caller", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		ReplyTo:        required.Item.ID,
		IdempotencyKey: "payment-proof-1",
	}, PaymentProofPayload{
		SignedTx: "signed-tx-bytes",
		Chain:    "solana:testnet",
		Amount:   "2000000",
		Nonce:    "nonce-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if proofDup.Item.ID != proof.Item.ID {
		t.Fatalf("duplicate proof created new item %s != %s", proofDup.Item.ID, proof.Item.ID)
	}
	if got := store.ListByRequestID("req-pay-1"); len(got) != 2 {
		t.Fatalf("request item count = %d, want 2 after duplicate proof", len(got))
	}

	store, err = NewStore(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.CreateResult(ctx, Item{
		From:           Principal{ID: "agent:provider", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		ReplyTo:        proof.Item.ID,
		IdempotencyKey: "result-1",
	}, ResultPayload{
		Data: json.RawMessage(`{"findings":["a","b"]}`),
		Receipt: ReceiptPayload{
			TxHash:            "tx-1",
			Caller:            "agent:caller",
			Provider:          "agent:provider",
			Method:            "research",
			Amount:            "2000000",
			Chain:             "solana:testnet",
			Nonce:             "nonce-1",
			ProviderRating:    5,
			ProviderSignature: "provider-sig",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	store, err = NewStore(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := store.CreateReceipt(ctx, Item{
		From:           Principal{ID: "agent:caller", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		ReplyTo:        result.Item.ID,
		IdempotencyKey: "receipt-1",
	}, ReceiptPayload{
		TxHash:            "tx-1",
		Caller:            "agent:caller",
		Provider:          "agent:provider",
		Method:            "research",
		Amount:            "2000000",
		Chain:             "solana:testnet",
		Nonce:             "nonce-1",
		CallerRating:      5,
		ProviderRating:    5,
		CallerSignature:   "caller-sig",
		ProviderSignature: "provider-sig",
	})
	if err != nil {
		t.Fatal(err)
	}
	receiptDup, err := store.CreateReceipt(ctx, Item{
		From:           Principal{ID: "agent:caller", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		ReplyTo:        result.Item.ID,
		IdempotencyKey: "receipt-1",
	}, ReceiptPayload{
		TxHash:            "tx-1",
		Caller:            "agent:caller",
		Provider:          "agent:provider",
		Method:            "research",
		Amount:            "2000000",
		Chain:             "solana:testnet",
		Nonce:             "nonce-1",
		CallerRating:      5,
		ProviderRating:    5,
		CallerSignature:   "caller-sig",
		ProviderSignature: "provider-sig",
	})
	if err != nil {
		t.Fatal(err)
	}
	if receiptDup.Item.ID != receipt.Item.ID {
		t.Fatalf("duplicate receipt created new item %s != %s", receiptDup.Item.ID, receipt.Item.ID)
	}

	records := store.ListByRequestID("req-pay-1")
	if len(records) != 4 {
		t.Fatalf("request item count = %d, want 4", len(records))
	}
	for _, record := range records {
		if record.State != StateCompleted {
			t.Fatalf("record %s state = %s, want %s", record.Item.Kind, record.State, StateCompleted)
		}
	}
}

func TestPaymentWorkflowRejectsNonceMismatchProof(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, _ := newTestMailboxStore(t)

	required, err := store.CreatePaymentRequired(ctx, Item{
		From:           Principal{ID: "agent:provider", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		To:             &Principal{ID: "agent:caller", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		RequestID:      "req-pay-2",
		IdempotencyKey: "payment-required-2",
	}, PaymentRequiredPayload{
		Method:  "research",
		Amount:  "100",
		Asset:   "USDC",
		Chain:   "solana:testnet",
		Address: "dest",
		Nonce:   "nonce-expected",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.CreatePaymentProof(ctx, Item{
		From:           Principal{ID: "agent:caller", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		ReplyTo:        required.Item.ID,
		IdempotencyKey: "payment-proof-bad",
	}, PaymentProofPayload{
		SignedTx: "signed",
		Chain:    "solana:testnet",
		Amount:   "100",
		Nonce:    "wrong-nonce",
	})
	if err == nil {
		t.Fatal("expected nonce mismatch error")
	}
}
