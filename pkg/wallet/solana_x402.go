package wallet

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
)

// memoProgram is the Solana SPL Memo program. The x402 spec lets
// servers attach a UTF-8 memo (≤256 bytes) to the transfer for
// reconciliation; the OWS x402 path includes it as a separate
// instruction when present.
const memoProgram = "MemoSq4gqABAXKb96qnH8TysNcWxMyWCqXgDLGmfcHr"

// BuildX402SolanaTransferTx constructs an unsigned v0 versioned
// Solana transaction matching the x402 "exact" scheme on SVM.
//
// The transaction has two signer slots:
//
//   - slot 0: the facilitator's `feePayer` pubkey (left as zeros;
//     the facilitator signs server-side after verification)
//   - slot 1: the client's `from` pubkey (OWS fills this slot when
//     it signs the bytes)
//
// The instruction layout is one SPL Token Transfer from the client's
// associated token account to the recipient's ATA, optionally
// preceded by a memo instruction when memo is non-empty.
//
// Returns hex-encoded bytes ready for `ows sign tx --chain solana
// --tx <hex>`. Both the client's and recipient's ATAs must already
// exist; this builder does not include CreateATA instructions
// because x402 services generally have a long-standing receiving
// account.
func BuildX402SolanaTransferTx(
	ctx context.Context,
	from, to, feePayer, mint string,
	amount uint64,
	memo string,
) (string, error) {
	if amount == 0 {
		return "", errors.New("amount must be positive")
	}
	fromKey, err := base58Decode(from)
	if err != nil {
		return "", fmt.Errorf("decoding sender: %w", err)
	}
	toKey, err := base58Decode(to)
	if err != nil {
		return "", fmt.Errorf("decoding recipient: %w", err)
	}
	feePayerKey, err := base58Decode(feePayer)
	if err != nil {
		return "", fmt.Errorf("decoding fee payer: %w", err)
	}
	mintKey, err := base58Decode(mint)
	if err != nil {
		return "", fmt.Errorf("decoding mint: %w", err)
	}
	tokProgKey, err := base58Decode(tokenProgram)
	if err != nil {
		return "", err
	}

	srcATA, err := findAssociatedTokenAddress(fromKey, mintKey, tokProgKey, mustB58(ataProgram))
	if err != nil {
		return "", fmt.Errorf("computing sender ATA: %w", err)
	}
	dstATA, err := findAssociatedTokenAddress(toKey, mintKey, tokProgKey, mustB58(ataProgram))
	if err != nil {
		return "", fmt.Errorf("computing recipient ATA: %w", err)
	}

	blockhash, err := getLatestBlockhash(ctx)
	if err != nil {
		return "", err
	}
	bhBytes, err := base58Decode(blockhash)
	if err != nil {
		return "", err
	}

	// SPL Token Transfer instruction data: [3 (u8 = Transfer), amount (u64 LE)].
	transferData := make([]byte, 9)
	transferData[0] = 3
	binary.LittleEndian.PutUint64(transferData[1:9], amount)

	// Account ordering (Solana convention requires signers first,
	// then writable non-signers, then readonly):
	//
	//   0  feePayer       writable signer  (must be first)
	//   1  client (from)  writable signer
	//   2  srcATA         writable
	//   3  dstATA         writable
	//   4  tokenProgram   readonly
	//   5  memoProgram    readonly        (only when memo set)
	//
	// Header is (numRequiredSignatures, numReadonlySigned,
	// numReadonlyUnsigned).
	hasMemo := len(memo) > 0
	if hasMemo && len(memo) > 256 {
		return "", errors.New("memo must be <= 256 bytes")
	}

	var msg bytes.Buffer

	// version prefix for v0 — high bit set on the first byte.
	msg.WriteByte(0x80)

	// header
	if hasMemo {
		msg.Write([]byte{2, 0, 2}) // 2 signers, 0 readonly-signed, 2 readonly-unsigned
	} else {
		msg.Write([]byte{2, 0, 1}) // 2 signers, 0 readonly-signed, 1 readonly-unsigned
	}

	// account keys
	keys := [][]byte{feePayerKey, fromKey, srcATA, dstATA, tokProgKey}
	if hasMemo {
		keys = append(keys, mustB58(memoProgram))
	}
	msg.Write(compactU16(len(keys)))
	for _, k := range keys {
		msg.Write(k)
	}

	// recent blockhash
	msg.Write(bhBytes)

	// instructions
	instructionCount := 1
	if hasMemo {
		instructionCount = 2
	}
	msg.Write(compactU16(instructionCount))

	if hasMemo {
		// Memo instruction: program=memoProgram (index 5), no
		// account references, data = memo bytes.
		msg.WriteByte(5)
		msg.Write(compactU16(0))
		msg.Write(compactU16(len(memo)))
		msg.WriteString(memo)
	}

	// SPL Token Transfer instruction: program=tokenProgram (index
	// 4), accounts=[srcATA(2), dstATA(3), client(1)], data=transfer.
	msg.WriteByte(4)
	msg.Write(compactU16(3))
	msg.Write([]byte{2, 3, 1})
	msg.Write(compactU16(len(transferData)))
	msg.Write(transferData)

	// v0 address-table-lookups (none).
	msg.Write(compactU16(0))

	// Assemble unsigned transaction: 2 zero signature placeholders
	// + message.
	var tx bytes.Buffer
	tx.Write(compactU16(2))
	tx.Write(make([]byte, 64*2))
	tx.Write(msg.Bytes())

	return hex.EncodeToString(tx.Bytes()), nil
}

// mustB58 panics if the canonical Solana program address fails to
// decode. Used only for the static program constants in this file.
func mustB58(s string) []byte {
	b, err := base58Decode(s)
	if err != nil {
		panic(fmt.Sprintf("invalid hard-coded base58 %q: %v", s, err))
	}
	return b
}
