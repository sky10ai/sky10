package wallet

import (
	"bytes"
	"context"
	cryptoRand "crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
)

// memoProgram is the Solana SPL Memo program. The x402 spec lets
// servers attach a UTF-8 memo (≤256 bytes) to the transfer for
// reconciliation; we include it as a separate instruction with a
// random nonce when the caller doesn't supply one (the reference
// SVM scheme requires the memo).
const memoProgram = "MemoSq4gqABAXKb96qnH8TysNcWxMyWCqXgDLGmfcHr"

// computeBudgetProgram is the Solana program that handles per-tx
// compute unit limit and price. The x402 SVM exact scheme expects
// SetComputeUnitLimit + SetComputeUnitPrice instructions at the
// start of every transfer transaction so facilitators can bound
// settlement cost.
const computeBudgetProgram = "ComputeBudget111111111111111111111111111111"

// Defaults pulled from the Coinbase reference (go/mechanisms/svm/
// constants.go). Facilitators reject larger compute-unit prices
// than 5_000_000 microlamports.
const (
	defaultComputeUnitLimit          uint32 = 20000
	defaultComputeUnitPriceMicrolamp uint64 = 1
)

// BuildX402SolanaTransferTx constructs an unsigned v0 versioned
// Solana transaction matching the x402 "exact" scheme on SVM.
//
// The transaction has two signer slots:
//
//   - slot 0: the facilitator's `feePayer` pubkey (left as zeros;
//     the facilitator signs server-side after verification)
//   - slot 1: the client's `from` pubkey (the caller fills this
//     slot after signing the message bytes returned alongside)
//
// The instruction layout is one SPL Token Transfer from the client's
// associated token account to the recipient's ATA, optionally
// preceded by a memo instruction when memo is non-empty.
//
// Returns the full unsigned transaction (with placeholder signature
// slots) and the message-only bytes — both as hex. Solana signers
// sign the message bytes; the full-tx form is what the wire payload
// looks like once the client's signature is inserted into slot 1.
// Both the client's and recipient's ATAs must already exist; this
// builder does not include CreateATA instructions because x402
// services generally have a long-standing receiving account.
func BuildX402SolanaTransferTx(
	ctx context.Context,
	from, to, feePayer, mint string,
	amount uint64,
	memo string,
) (fullUnsignedHex, messageHex string, _ error) {
	if amount == 0 {
		return "", "", errors.New("amount must be positive")
	}
	fromKey, err := base58Decode(from)
	if err != nil {
		return "", "", fmt.Errorf("decoding sender: %w", err)
	}
	toKey, err := base58Decode(to)
	if err != nil {
		return "", "", fmt.Errorf("decoding recipient: %w", err)
	}
	feePayerKey, err := base58Decode(feePayer)
	if err != nil {
		return "", "", fmt.Errorf("decoding fee payer: %w", err)
	}
	mintKey, err := base58Decode(mint)
	if err != nil {
		return "", "", fmt.Errorf("decoding mint: %w", err)
	}
	tokProgKey, err := base58Decode(tokenProgram)
	if err != nil {
		return "", "", err
	}

	srcATA, err := findAssociatedTokenAddress(fromKey, mintKey, tokProgKey, mustB58(ataProgram))
	if err != nil {
		return "", "", fmt.Errorf("computing sender ATA: %w", err)
	}
	dstATA, err := findAssociatedTokenAddress(toKey, mintKey, tokProgKey, mustB58(ataProgram))
	if err != nil {
		return "", "", fmt.Errorf("computing recipient ATA: %w", err)
	}

	blockhash, err := getLatestBlockhash(ctx)
	if err != nil {
		return "", "", err
	}
	bhBytes, err := base58Decode(blockhash)
	if err != nil {
		return "", "", err
	}

	// USDC has 6 decimals. The asset address is the mint pubkey;
	// for x402 today we only settle USDC mainnet so we hard-code
	// decimals to 6 rather than fetching the mint account at sign
	// time. If we ever support other SPL tokens we'd resolve
	// decimals from the mint.
	const usdcDecimals = 6

	// SPL Token TransferChecked instruction data: [12 (u8), amount
	// (u64 LE), decimals (u8)]. TransferChecked is what the x402 SVM
	// reference uses — raw Transfer (instr 3) skips mint validation
	// and gets rejected by some facilitators.
	transferData := make([]byte, 1+8+1)
	transferData[0] = 12
	binary.LittleEndian.PutUint64(transferData[1:9], amount)
	transferData[9] = usdcDecimals

	// Memo is required by the reference SVM exact scheme. When the
	// caller doesn't supply one, we fill with a random hex nonce
	// for replay-uniqueness. Cap at the spec's 256-byte limit.
	memoBytes := []byte(memo)
	if len(memoBytes) == 0 {
		nonce := make([]byte, 16)
		if _, err := cryptoRand.Read(nonce); err != nil {
			return "", "", fmt.Errorf("memo nonce: %w", err)
		}
		memoBytes = []byte(hex.EncodeToString(nonce))
	}
	if len(memoBytes) > 256 {
		return "", "", errors.New("memo must be <= 256 bytes")
	}

	// Account ordering (Solana convention: signers first, then
	// writable non-signers, then readonly):
	//
	//   0  feePayer            writable signer  (must be first)
	//   1  client (from)       writable signer
	//   2  srcATA              writable non-signer
	//   3  dstATA              writable non-signer
	//   4  mint                readonly non-signer  (TransferChecked needs it)
	//   5  tokenProgram        readonly non-signer
	//   6  memoProgram         readonly non-signer
	//   7  computeBudgetProgram readonly non-signer
	//
	// Header is (numRequiredSignatures, numReadonlySigned,
	// numReadonlyUnsigned). 2/0/4 covers indices 4..7.
	keys := [][]byte{
		feePayerKey, fromKey, // signers
		srcATA, dstATA, // writable non-signers
		mintKey, tokProgKey, mustB58(memoProgram), mustB58(computeBudgetProgram), // readonly non-signers
	}

	// Compute Budget instructions
	cuLimitData := make([]byte, 1+4)
	cuLimitData[0] = 2 // SetComputeUnitLimit discriminator
	binary.LittleEndian.PutUint32(cuLimitData[1:], defaultComputeUnitLimit)

	cuPriceData := make([]byte, 1+8)
	cuPriceData[0] = 3 // SetComputeUnitPrice discriminator
	binary.LittleEndian.PutUint64(cuPriceData[1:], defaultComputeUnitPriceMicrolamp)

	var msg bytes.Buffer

	// version prefix for v0 — high bit set on the first byte.
	msg.WriteByte(0x80)
	// header: 2 signers, 0 readonly-signed, 4 readonly-unsigned
	msg.Write([]byte{2, 0, 4})

	msg.Write(compactU16(len(keys)))
	for _, k := range keys {
		msg.Write(k)
	}

	// recent blockhash
	msg.Write(bhBytes)

	// 4 instructions in the order the Coinbase reference SVM client
	// emits: SetComputeUnitLimit, SetComputeUnitPrice,
	// TransferChecked, Memo. Facilitators detect the payment by
	// matching the third instruction's program/discriminator
	// against TransferChecked, so memo MUST come last.
	msg.Write(compactU16(4))

	// SetComputeUnitLimit: program=computeBudgetProgram (index 7),
	// no accounts, data=[2, limit_u32_le]
	msg.WriteByte(7)
	msg.Write(compactU16(0))
	msg.Write(compactU16(len(cuLimitData)))
	msg.Write(cuLimitData)

	// SetComputeUnitPrice: program=computeBudgetProgram (index 7),
	// no accounts, data=[3, microlamports_u64_le]
	msg.WriteByte(7)
	msg.Write(compactU16(0))
	msg.Write(compactU16(len(cuPriceData)))
	msg.Write(cuPriceData)

	// SPL Token TransferChecked instruction: program=tokenProgram
	// (index 5), accounts=[srcATA(2), mint(4), dstATA(3), client(1)],
	// data=[12, amount, decimals].
	msg.WriteByte(5)
	msg.Write(compactU16(4))
	msg.Write([]byte{2, 4, 3, 1})
	msg.Write(compactU16(len(transferData)))
	msg.Write(transferData)

	// Memo instruction: program=memoProgram (index 6), no account
	// references, data = memo bytes.
	msg.WriteByte(6)
	msg.Write(compactU16(0))
	msg.Write(compactU16(len(memoBytes)))
	msg.Write(memoBytes)

	// v0 address-table-lookups (none).
	msg.Write(compactU16(0))

	messageBytes := msg.Bytes()

	// Assemble unsigned transaction: 2 zero signature placeholders
	// + message.
	var tx bytes.Buffer
	tx.Write(compactU16(2))
	tx.Write(make([]byte, 64*2))
	tx.Write(messageBytes)

	return hex.EncodeToString(tx.Bytes()), hex.EncodeToString(messageBytes), nil
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
