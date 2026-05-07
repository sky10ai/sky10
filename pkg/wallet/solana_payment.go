package wallet

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
)

// Exported Solana program IDs used by payment protocols.
const (
	SolanaTokenProgram     = tokenProgram
	SolanaToken2022Program = "TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb"
)

// SolanaPaymentTxOptions describes a protocol-neutral Solana payment
// transaction. An empty Mint means native SOL; a non-empty Mint means SPL or
// Token-2022 TransferChecked using TokenProgram and Decimals.
type SolanaPaymentTxOptions struct {
	From                          string
	Recipient                     string
	FeePayer                      string
	Mint                          string
	TokenProgram                  string
	Amount                        uint64
	Decimals                      uint8
	Memo                          string
	RecentBlockhash               string
	ComputeUnitLimit              uint32
	ComputeUnitPriceMicrolamports uint64
	Splits                        []SolanaPaymentSplit
}

// SolanaPaymentSplit is an additional transfer in the same asset.
type SolanaPaymentSplit struct {
	Recipient string
	Amount    uint64
	Memo      string
	CreateATA bool
}

// SolanaPaymentTx is an unsigned serialized Solana transaction plus the
// signature slot the payer wallet must fill.
type SolanaPaymentTx struct {
	FullUnsignedHex string
	MessageHex      string
	SignerSlot      int
}

// BuildSolanaPaymentTx constructs a legacy Solana transaction for MPP-style
// charge payments. The transaction is unsigned; callers sign the message and
// insert the 64-byte signature into SignerSlot.
func BuildSolanaPaymentTx(ctx context.Context, opts SolanaPaymentTxOptions) (*SolanaPaymentTx, error) {
	if opts.Amount == 0 {
		return nil, errors.New("amount must be positive")
	}
	fromKey, err := decodePaymentPubkey("sender", opts.From)
	if err != nil {
		return nil, err
	}
	recipientKey, err := decodePaymentPubkey("recipient", opts.Recipient)
	if err != nil {
		return nil, err
	}
	payerKey := fromKey
	if opts.FeePayer != "" && opts.FeePayer != opts.From {
		payerKey, err = decodePaymentPubkey("fee payer", opts.FeePayer)
		if err != nil {
			return nil, err
		}
	}

	blockhash := opts.RecentBlockhash
	if blockhash == "" {
		blockhash, err = getLatestBlockhash(ctx)
		if err != nil {
			return nil, err
		}
	}
	blockhashKey, err := decodePaymentPubkey("blockhash", blockhash)
	if err != nil {
		return nil, err
	}

	instructions, err := buildPaymentInstructions(fromKey, recipientKey, payerKey, opts)
	if err != nil {
		return nil, err
	}
	message, signerSlot, sigCount, err := compilePaymentMessage(payerKey, fromKey, blockhashKey, instructions)
	if err != nil {
		return nil, err
	}

	var tx bytes.Buffer
	tx.Write(compactU16(sigCount))
	tx.Write(make([]byte, 64*sigCount))
	tx.Write(message)

	return &SolanaPaymentTx{
		FullUnsignedHex: hex.EncodeToString(tx.Bytes()),
		MessageHex:      hex.EncodeToString(message),
		SignerSlot:      signerSlot,
	}, nil
}

func buildPaymentInstructions(fromKey, recipientKey, payerKey []byte, opts SolanaPaymentTxOptions) ([]paymentInstruction, error) {
	primaryAmount, err := primaryPaymentAmount(opts.Amount, opts.Splits)
	if err != nil {
		return nil, err
	}
	cuPrice := opts.ComputeUnitPriceMicrolamports
	if cuPrice == 0 {
		cuPrice = 1
	}
	cuLimit := opts.ComputeUnitLimit
	if cuLimit == 0 {
		cuLimit = 200_000
	}

	instructions := []paymentInstruction{
		computeUnitPriceInstruction(cuPrice),
		computeUnitLimitInstruction(cuLimit),
	}

	if opts.Mint == "" {
		instructions = append(instructions, systemTransferInstruction(fromKey, recipientKey, primaryAmount))
		if memo, err := memoInstruction(opts.Memo); err != nil {
			return nil, err
		} else if memo != nil {
			instructions = append(instructions, *memo)
		}
		for _, split := range opts.Splits {
			splitRecipient, err := decodePaymentPubkey("split recipient", split.Recipient)
			if err != nil {
				return nil, err
			}
			if split.Amount == 0 {
				return nil, errors.New("split amount must be positive")
			}
			instructions = append(instructions, systemTransferInstruction(fromKey, splitRecipient, split.Amount))
			if memo, err := memoInstruction(split.Memo); err != nil {
				return nil, err
			} else if memo != nil {
				instructions = append(instructions, *memo)
			}
		}
		return instructions, nil
	}

	tokenProgramValue := opts.TokenProgram
	if tokenProgramValue == "" {
		tokenProgramValue = SolanaTokenProgram
	}
	mintKey, err := decodePaymentPubkey("mint", opts.Mint)
	if err != nil {
		return nil, err
	}
	tokenProgramKey, err := decodePaymentPubkey("token program", tokenProgramValue)
	if err != nil {
		return nil, err
	}
	ataProgramKey := mustB58(ataProgram)
	sourceATA, err := findAssociatedTokenAddress(fromKey, mintKey, tokenProgramKey, ataProgramKey)
	if err != nil {
		return nil, fmt.Errorf("computing sender ATA: %w", err)
	}
	recipientATA, err := findAssociatedTokenAddress(recipientKey, mintKey, tokenProgramKey, ataProgramKey)
	if err != nil {
		return nil, fmt.Errorf("computing recipient ATA: %w", err)
	}

	instructions = append(instructions, tokenTransferCheckedInstruction(
		sourceATA, mintKey, recipientATA, fromKey, tokenProgramKey, primaryAmount, opts.Decimals,
	))
	if memo, err := memoInstruction(opts.Memo); err != nil {
		return nil, err
	} else if memo != nil {
		instructions = append(instructions, *memo)
	}
	for _, split := range opts.Splits {
		splitRecipient, err := decodePaymentPubkey("split recipient", split.Recipient)
		if err != nil {
			return nil, err
		}
		if split.Amount == 0 {
			return nil, errors.New("split amount must be positive")
		}
		splitATA, err := findAssociatedTokenAddress(splitRecipient, mintKey, tokenProgramKey, ataProgramKey)
		if err != nil {
			return nil, fmt.Errorf("computing split recipient ATA: %w", err)
		}
		if split.CreateATA {
			instructions = append(instructions, createAssociatedTokenAccountInstruction(
				payerKey, splitATA, splitRecipient, mintKey, tokenProgramKey,
			))
		}
		instructions = append(instructions, tokenTransferCheckedInstruction(
			sourceATA, mintKey, splitATA, fromKey, tokenProgramKey, split.Amount, opts.Decimals,
		))
		if memo, err := memoInstruction(split.Memo); err != nil {
			return nil, err
		} else if memo != nil {
			instructions = append(instructions, *memo)
		}
	}
	return instructions, nil
}

func primaryPaymentAmount(total uint64, splits []SolanaPaymentSplit) (uint64, error) {
	remaining := total
	for _, split := range splits {
		if split.Amount == 0 {
			return 0, errors.New("split amount must be positive")
		}
		if split.Amount >= remaining {
			return 0, errors.New("splits must be less than total amount")
		}
		remaining -= split.Amount
	}
	if remaining == 0 {
		return 0, errors.New("primary amount must be positive")
	}
	return remaining, nil
}

func decodePaymentPubkey(label, value string) ([]byte, error) {
	key, err := base58Decode(value)
	if err != nil {
		return nil, fmt.Errorf("decoding %s: %w", label, err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("decoding %s: expected 32 bytes, got %d", label, len(key))
	}
	if base58Encode(key) != value {
		return nil, fmt.Errorf("decoding %s: non-canonical Solana pubkey", label)
	}
	return key, nil
}

type paymentAccountMeta struct {
	pubkey     []byte
	isSigner   bool
	isWritable bool
}

type paymentInstruction struct {
	programID []byte
	accounts  []paymentAccountMeta
	data      []byte
}

func computeUnitPriceInstruction(microlamports uint64) paymentInstruction {
	data := make([]byte, 1+8)
	data[0] = 3
	binary.LittleEndian.PutUint64(data[1:], microlamports)
	return paymentInstruction{programID: mustB58(computeBudgetProgram), data: data}
}

func computeUnitLimitInstruction(units uint32) paymentInstruction {
	data := make([]byte, 1+4)
	data[0] = 2
	binary.LittleEndian.PutUint32(data[1:], units)
	return paymentInstruction{programID: mustB58(computeBudgetProgram), data: data}
}

func systemTransferInstruction(from, to []byte, lamports uint64) paymentInstruction {
	data := make([]byte, 12)
	binary.LittleEndian.PutUint32(data[:4], 2)
	binary.LittleEndian.PutUint64(data[4:], lamports)
	return paymentInstruction{
		programID: mustB58(systemProgram),
		accounts: []paymentAccountMeta{
			{pubkey: from, isSigner: true, isWritable: true},
			{pubkey: to, isWritable: true},
		},
		data: data,
	}
}

func tokenTransferCheckedInstruction(source, mint, dest, owner, tokenProgram []byte, amount uint64, decimals uint8) paymentInstruction {
	data := make([]byte, 10)
	data[0] = 12
	binary.LittleEndian.PutUint64(data[1:9], amount)
	data[9] = decimals
	return paymentInstruction{
		programID: tokenProgram,
		accounts: []paymentAccountMeta{
			{pubkey: source, isWritable: true},
			{pubkey: mint},
			{pubkey: dest, isWritable: true},
			{pubkey: owner, isSigner: true},
		},
		data: data,
	}
}

func createAssociatedTokenAccountInstruction(payer, ata, owner, mint, tokenProgram []byte) paymentInstruction {
	return paymentInstruction{
		programID: mustB58(ataProgram),
		accounts: []paymentAccountMeta{
			{pubkey: payer, isSigner: true, isWritable: true},
			{pubkey: ata, isWritable: true},
			{pubkey: owner},
			{pubkey: mint},
			{pubkey: mustB58(systemProgram)},
			{pubkey: tokenProgram},
		},
		data: []byte{1},
	}
}

func memoInstruction(memo string) (*paymentInstruction, error) {
	if memo == "" {
		return nil, nil
	}
	data := []byte(memo)
	if len(data) > 566 {
		return nil, errors.New("memo cannot exceed 566 bytes")
	}
	return &paymentInstruction{programID: mustB58(memoProgram), data: data}, nil
}

type compiledPaymentAccount struct {
	pubkey     []byte
	isSigner   bool
	isWritable bool
}

func compilePaymentMessage(payerKey, signerKey, blockhash []byte, instructions []paymentInstruction) ([]byte, int, int, error) {
	accounts := make(map[string]*compiledPaymentAccount)
	order := make([]string, 0)
	add := func(pubkey []byte, isSigner, isWritable bool) {
		key := hex.EncodeToString(pubkey)
		if account, ok := accounts[key]; ok {
			account.isSigner = account.isSigner || isSigner
			account.isWritable = account.isWritable || isWritable
			return
		}
		cp := append([]byte(nil), pubkey...)
		accounts[key] = &compiledPaymentAccount{pubkey: cp, isSigner: isSigner, isWritable: isWritable}
		order = append(order, key)
	}

	add(payerKey, true, true)
	if !bytes.Equal(payerKey, signerKey) {
		add(signerKey, true, true)
	}
	for _, ix := range instructions {
		if len(ix.programID) != 32 {
			return nil, 0, 0, errors.New("instruction program id must be 32 bytes")
		}
		add(ix.programID, false, false)
		for _, account := range ix.accounts {
			if len(account.pubkey) != 32 {
				return nil, 0, 0, errors.New("instruction account pubkey must be 32 bytes")
			}
			add(account.pubkey, account.isSigner, account.isWritable)
		}
	}

	payerKeyStr := hex.EncodeToString(payerKey)
	appendGroup := func(dst []compiledPaymentAccount, pred func(*compiledPaymentAccount) bool) []compiledPaymentAccount {
		for _, key := range order {
			if key == payerKeyStr {
				continue
			}
			account := accounts[key]
			if pred(account) {
				dst = append(dst, *account)
			}
		}
		return dst
	}

	var ordered []compiledPaymentAccount
	ordered = append(ordered, *accounts[payerKeyStr])
	ordered = appendGroup(ordered, func(a *compiledPaymentAccount) bool { return a.isSigner && a.isWritable })
	readonlySignedStart := len(ordered)
	ordered = appendGroup(ordered, func(a *compiledPaymentAccount) bool { return a.isSigner && !a.isWritable })
	writableUnsignedStart := len(ordered)
	ordered = appendGroup(ordered, func(a *compiledPaymentAccount) bool { return !a.isSigner && a.isWritable })
	readonlyUnsignedStart := len(ordered)
	ordered = appendGroup(ordered, func(a *compiledPaymentAccount) bool { return !a.isSigner && !a.isWritable })

	index := make(map[string]int, len(ordered))
	signerSlot := -1
	sigCount := 0
	for i, account := range ordered {
		key := hex.EncodeToString(account.pubkey)
		index[key] = i
		if account.isSigner {
			if bytes.Equal(account.pubkey, signerKey) {
				signerSlot = sigCount
			}
			sigCount++
		}
	}
	if signerSlot < 0 {
		return nil, 0, 0, errors.New("sender signer not found in transaction accounts")
	}

	var msg bytes.Buffer
	msg.Write([]byte{
		byte(sigCount),
		byte(writableUnsignedStart - readonlySignedStart),
		byte(len(ordered) - readonlyUnsignedStart),
	})
	msg.Write(compactU16(len(ordered)))
	for _, account := range ordered {
		msg.Write(account.pubkey)
	}
	msg.Write(blockhash)
	msg.Write(compactU16(len(instructions)))
	for _, ix := range instructions {
		programIndex, ok := index[hex.EncodeToString(ix.programID)]
		if !ok {
			return nil, 0, 0, errors.New("program id missing from account list")
		}
		msg.WriteByte(byte(programIndex))
		msg.Write(compactU16(len(ix.accounts)))
		for _, account := range ix.accounts {
			accountIndex, ok := index[hex.EncodeToString(account.pubkey)]
			if !ok {
				return nil, 0, 0, errors.New("instruction account missing from account list")
			}
			msg.WriteByte(byte(accountIndex))
		}
		msg.Write(compactU16(len(ix.data)))
		msg.Write(ix.data)
	}
	return msg.Bytes(), signerSlot, sigCount, nil
}

// InsertSolanaSignature writes a 64-byte signature into a serialized unsigned
// transaction and returns the signed transaction bytes.
func InsertSolanaSignature(fullUnsignedHex string, signerSlot int, signature []byte) ([]byte, error) {
	if signerSlot < 0 {
		return nil, errors.New("signer slot must be non-negative")
	}
	if len(signature) != 64 {
		return nil, fmt.Errorf("expected 64-byte ed25519 signature, got %d bytes", len(signature))
	}
	tx, err := hex.DecodeString(stripHexPrefix(fullUnsignedHex))
	if err != nil {
		return nil, fmt.Errorf("decode unsigned transaction: %w", err)
	}
	sigCount, offset, err := readCompactU16(tx)
	if err != nil {
		return nil, err
	}
	if signerSlot >= sigCount {
		return nil, fmt.Errorf("signer slot %d outside %d signature slots", signerSlot, sigCount)
	}
	start := offset + signerSlot*64
	end := start + 64
	if len(tx) < end {
		return nil, fmt.Errorf("transaction too short for signature slot %d", signerSlot)
	}
	signed := append([]byte(nil), tx...)
	copy(signed[start:end], signature)
	return signed, nil
}

func stripHexPrefix(value string) string {
	if len(value) >= 2 && value[0] == '0' && (value[1] == 'x' || value[1] == 'X') {
		return value[2:]
	}
	return value
}

func readCompactU16(data []byte) (int, int, error) {
	var value int
	var shift uint
	for i := 0; i < len(data) && i < 3; i++ {
		b := data[i]
		value |= int(b&0x7f) << shift
		if b&0x80 == 0 {
			return value, i + 1, nil
		}
		shift += 7
	}
	return 0, 0, errors.New("invalid compact-u16")
}
