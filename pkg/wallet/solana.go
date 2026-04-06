// solana.go queries the Solana JSON-RPC directly for native SOL and
// SPL token balances. We bypass `ows fund balance` because as of OWS
// 1.2.4 it only reports SPL tokens, not native SOL.
package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
)

// Default Solana mainnet RPC endpoint (same as OWS default).
var solanaRPC = "https://api.mainnet-beta.solana.com"

// Well-known SPL token mints on Solana mainnet.
const usdcMint = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"

// Well-known program addresses.
const (
	tokenProgram   = "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
	ataProgram     = "ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL"
	lamportsPerSOL = 1_000_000_000
	usdcDecimals   = 6
)

// solanaBalances queries the Solana RPC for native SOL and SPL token
// balances for the given address. Returns a populated BalanceResult.
func solanaBalances(ctx context.Context, address string) (*BalanceResult, error) {
	result := &BalanceResult{
		Address: address,
		Chain:   ChainSolana,
	}

	// Fetch native SOL and SPL tokens in parallel.
	type solResult struct {
		lamports uint64
		err      error
	}
	type splResult struct {
		tokens []TokenBalance
		err    error
	}

	solCh := make(chan solResult, 1)
	splCh := make(chan splResult, 1)

	go func() {
		lamports, err := rpcGetBalance(ctx, address)
		solCh <- solResult{lamports, err}
	}()
	go func() {
		tokens, err := rpcGetTokenAccounts(ctx, address)
		splCh <- splResult{tokens, err}
	}()

	sol := <-solCh
	spl := <-splCh

	// Native SOL balance.
	solBalance := "0"
	if sol.err == nil && sol.lamports > 0 {
		solBalance = formatLamports(sol.lamports)
	}
	result.Tokens = append(result.Tokens, TokenBalance{
		Symbol:  "SOL",
		Balance: solBalance,
	})

	// SPL tokens — always show USDC even if zero.
	usdcFound := false
	if spl.err == nil {
		for _, t := range spl.tokens {
			result.Tokens = append(result.Tokens, t)
			if t.Mint == usdcMint {
				usdcFound = true
			}
		}
	}
	if !usdcFound {
		result.Tokens = append(result.Tokens, TokenBalance{
			Symbol:  "USDC",
			Balance: "0",
			Mint:    usdcMint,
		})
	}

	return result, nil
}

// formatLamports converts lamports to a human-readable SOL string.
func formatLamports(lamports uint64) string {
	whole := lamports / lamportsPerSOL
	frac := lamports % lamportsPerSOL
	if frac == 0 {
		return strconv.FormatUint(whole, 10)
	}
	// Format with up to 9 decimal places, trim trailing zeros.
	s := fmt.Sprintf("%d.%09d", whole, frac)
	s = trimTrailingZeros(s)
	return s
}

func trimTrailingZeros(s string) string {
	i := len(s) - 1
	for i > 0 && s[i] == '0' {
		i--
	}
	if s[i] == '.' {
		i--
	}
	return s[:i+1]
}

// rpcGetBalance calls Solana getBalance for native SOL.
func rpcGetBalance(ctx context.Context, address string) (uint64, error) {
	body, err := solanaRPCCall(ctx, "getBalance", []interface{}{address})
	if err != nil {
		return 0, err
	}
	var resp struct {
		Result struct {
			Value uint64 `json:"value"`
		} `json:"result"`
		Error *rpcError `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, fmt.Errorf("parsing getBalance: %w", err)
	}
	if resp.Error != nil {
		return 0, fmt.Errorf("getBalance: %s", resp.Error.Message)
	}
	return resp.Result.Value, nil
}

// rpcGetTokenAccounts calls getTokenAccountsByOwner for SPL tokens.
func rpcGetTokenAccounts(ctx context.Context, address string) ([]TokenBalance, error) {
	params := []interface{}{
		address,
		map[string]string{"programId": "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"},
		map[string]string{"encoding": "jsonParsed"},
	}
	body, err := solanaRPCCall(ctx, "getTokenAccountsByOwner", params)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Result struct {
			Value []struct {
				Account struct {
					Data struct {
						Parsed struct {
							Info struct {
								Mint        string `json:"mint"`
								TokenAmount struct {
									UIAmountString string `json:"uiAmountString"`
								} `json:"tokenAmount"`
							} `json:"info"`
						} `json:"parsed"`
					} `json:"data"`
				} `json:"account"`
			} `json:"value"`
		} `json:"result"`
		Error *rpcError `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing getTokenAccountsByOwner: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("getTokenAccounts: %s", resp.Error.Message)
	}

	var tokens []TokenBalance
	for _, v := range resp.Result.Value {
		info := v.Account.Data.Parsed.Info
		symbol := mintSymbol(info.Mint)
		tokens = append(tokens, TokenBalance{
			Symbol:  symbol,
			Balance: info.TokenAmount.UIAmountString,
			Mint:    info.Mint,
		})
	}
	return tokens, nil
}

// mintSymbol returns a human-readable symbol for known mints.
func mintSymbol(mint string) string {
	switch mint {
	case usdcMint:
		return "USDC"
	default:
		// Abbreviate unknown mints.
		if len(mint) > 8 {
			return mint[:4] + "..." + mint[len(mint)-4:]
		}
		return mint
	}
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// EstimatedFee is a fallback fee estimate in lamports if getFeeForMessage fails.
const EstimatedFee uint64 = 10_000 // 0.00001 SOL

// Solana System Program address (32 zero bytes in base58).
const systemProgram = "11111111111111111111111111111111"

// buildSOLTransferTx builds an unsigned Solana transaction for a native
// SOL transfer and returns the serialized bytes. The transaction uses
// the legacy message format with a single SystemProgram.Transfer instruction.
func buildSOLTransferTx(ctx context.Context, from, to string, lamports uint64) ([]byte, error) {
	fromKey, err := base58Decode(from)
	if err != nil {
		return nil, fmt.Errorf("decoding sender: %w", err)
	}
	toKey, err := base58Decode(to)
	if err != nil {
		return nil, fmt.Errorf("decoding recipient: %w", err)
	}
	sysKey, err := base58Decode(systemProgram)
	if err != nil {
		return nil, fmt.Errorf("decoding system program: %w", err)
	}

	blockhash, err := getLatestBlockhash(ctx)
	if err != nil {
		return nil, err
	}
	bhBytes, err := base58Decode(blockhash)
	if err != nil {
		return nil, fmt.Errorf("decoding blockhash: %w", err)
	}

	// SystemProgram.Transfer instruction data: [u32 LE index=2, u64 LE lamports].
	instrData := make([]byte, 12)
	binary.LittleEndian.PutUint32(instrData[0:4], 2)
	binary.LittleEndian.PutUint64(instrData[4:12], lamports)

	// Build the message.
	var msg bytes.Buffer

	// Header: num_required_signatures, num_readonly_signed, num_readonly_unsigned.
	msg.Write([]byte{1, 0, 1})

	// Account keys: [from (signer+writable), to (writable), system_program (readonly)].
	msg.Write(compactU16(3))
	msg.Write(fromKey)
	msg.Write(toKey)
	msg.Write(sysKey)

	// Recent blockhash.
	msg.Write(bhBytes)

	// Instructions: 1 instruction.
	msg.Write(compactU16(1))
	// program_id_index = 2 (system_program).
	msg.WriteByte(2)
	// account indices: [0 (from), 1 (to)].
	msg.Write(compactU16(2))
	msg.Write([]byte{0, 1})
	// instruction data.
	msg.Write(compactU16(len(instrData)))
	msg.Write(instrData)

	// Build unsigned transaction: num_signatures + zero signature + message.
	var tx bytes.Buffer
	tx.Write(compactU16(1))
	tx.Write(make([]byte, 64)) // placeholder signature
	tx.Write(msg.Bytes())

	return tx.Bytes(), nil
}

// maxSOLTransfer returns the maximum SOL that can be sent from an address,
// accounting for the exact transaction fee.
func maxSOLTransfer(ctx context.Context, from string) (uint64, uint64, error) {
	// Get current balance.
	balance, err := rpcGetBalance(ctx, from)
	if err != nil {
		return 0, 0, err
	}

	// Build a dummy transfer to compute the fee.
	// Destination doesn't matter for fee calculation.
	dummy := from // send to self
	txBytes, err := buildSOLTransferTx(ctx, from, dummy, 1)
	if err != nil {
		// Fall back to estimated fee.
		fee := EstimatedFee
		if balance <= fee {
			return 0, fee, nil
		}
		return balance - fee, fee, nil
	}

	// Extract the message (skip 1 byte compact-u16 + 64 byte signature placeholder).
	msg := txBytes[65:]
	fee, err := getFeeForMessage(ctx, msg)
	if err != nil {
		fee = EstimatedFee
	}

	if balance <= fee {
		return 0, fee, nil
	}
	return balance - fee, fee, nil
}

// getFeeForMessage queries the exact fee for a serialized message.
func getFeeForMessage(ctx context.Context, message []byte) (uint64, error) {
	// Solana expects base64-encoded message.
	import64 := base64.StdEncoding.EncodeToString(message)
	body, err := solanaRPCCall(ctx, "getFeeForMessage", []interface{}{
		import64,
		map[string]string{"commitment": "confirmed"},
	})
	if err != nil {
		return 0, err
	}
	var resp struct {
		Result struct {
			Value *uint64 `json:"value"`
		} `json:"result"`
		Error *rpcError `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, fmt.Errorf("parsing getFeeForMessage: %w", err)
	}
	if resp.Error != nil {
		return 0, fmt.Errorf("getFeeForMessage: %s", resp.Error.Message)
	}
	if resp.Result.Value == nil {
		return 0, fmt.Errorf("getFeeForMessage: null fee")
	}
	return *resp.Result.Value, nil
}

// buildSPLTransferTx builds an unsigned Solana transaction to transfer
// SPL tokens (e.g. USDC). If the recipient doesn't have an associated
// token account, a CreateAssociatedTokenAccount instruction is prepended.
func buildSPLTransferTx(ctx context.Context, from, to, mint string, amount uint64) ([]byte, error) {
	fromKey, err := base58Decode(from)
	if err != nil {
		return nil, fmt.Errorf("decoding sender: %w", err)
	}
	toKey, err := base58Decode(to)
	if err != nil {
		return nil, fmt.Errorf("decoding recipient: %w", err)
	}
	mintKey, err := base58Decode(mint)
	if err != nil {
		return nil, fmt.Errorf("decoding mint: %w", err)
	}
	tokProgKey, err := base58Decode(tokenProgram)
	if err != nil {
		return nil, err
	}
	ataProgKey, err := base58Decode(ataProgram)
	if err != nil {
		return nil, err
	}
	sysKey, err := base58Decode(systemProgram)
	if err != nil {
		return nil, err
	}

	// Compute ATAs for sender and recipient.
	srcATA, err := findAssociatedTokenAddress(fromKey, mintKey, tokProgKey, ataProgKey)
	if err != nil {
		return nil, fmt.Errorf("computing sender ATA: %w", err)
	}
	dstATA, err := findAssociatedTokenAddress(toKey, mintKey, tokProgKey, ataProgKey)
	if err != nil {
		return nil, fmt.Errorf("computing recipient ATA: %w", err)
	}

	blockhash, err := getLatestBlockhash(ctx)
	if err != nil {
		return nil, err
	}
	bhBytes, err := base58Decode(blockhash)
	if err != nil {
		return nil, err
	}

	// Check if recipient ATA exists.
	needCreate, err := accountMissing(ctx, dstATA)
	if err != nil {
		return nil, fmt.Errorf("checking recipient ATA: %w", err)
	}

	// SPL Token Transfer instruction data: [3 (u8), amount (u64 LE)].
	transferData := make([]byte, 9)
	transferData[0] = 3 // Transfer instruction
	binary.LittleEndian.PutUint64(transferData[1:9], amount)

	var msg bytes.Buffer
	if needCreate {
		// CreateATA + Transfer.
		// Accounts: [sender(S,W), dstATA(W), srcATA(W), recipient(R), mint(R), sysProg(R), tokProg(R), ataProg(R)]
		msg.Write([]byte{1, 0, 5}) // 1 signer, 0 readonly-signed, 5 readonly-unsigned
		msg.Write(compactU16(8))
		msg.Write(fromKey)
		msg.Write(dstATA)
		msg.Write(srcATA)
		msg.Write(toKey)
		msg.Write(mintKey)
		msg.Write(sysKey)
		msg.Write(tokProgKey)
		msg.Write(ataProgKey)
		msg.Write(bhBytes)

		// 2 instructions.
		msg.Write(compactU16(2))

		// Instruction 1: CreateAssociatedTokenAccount.
		msg.WriteByte(7) // program_id_index = ataProg
		msg.Write(compactU16(6))
		msg.Write([]byte{0, 1, 3, 4, 5, 6}) // payer, ata, wallet, mint, sysProg, tokProg
		msg.Write(compactU16(0))            // no data

		// Instruction 2: Token Transfer.
		msg.WriteByte(6) // program_id_index = tokProg
		msg.Write(compactU16(3))
		msg.Write([]byte{2, 1, 0}) // source, dest, owner
		msg.Write(compactU16(len(transferData)))
		msg.Write(transferData)
	} else {
		// Transfer only.
		// Accounts: [sender(S,W), srcATA(W), dstATA(W), tokProg(R)]
		msg.Write([]byte{1, 0, 1}) // 1 signer, 0 readonly-signed, 1 readonly-unsigned
		msg.Write(compactU16(4))
		msg.Write(fromKey)
		msg.Write(srcATA)
		msg.Write(dstATA)
		msg.Write(tokProgKey)
		msg.Write(bhBytes)

		// 1 instruction.
		msg.Write(compactU16(1))
		msg.WriteByte(3) // program_id_index = tokProg
		msg.Write(compactU16(3))
		msg.Write([]byte{1, 2, 0}) // source, dest, owner
		msg.Write(compactU16(len(transferData)))
		msg.Write(transferData)
	}

	// Unsigned transaction.
	var tx bytes.Buffer
	tx.Write(compactU16(1))
	tx.Write(make([]byte, 64))
	tx.Write(msg.Bytes())
	return tx.Bytes(), nil
}

// findAssociatedTokenAddress computes the ATA address (a PDA).
func findAssociatedTokenAddress(owner, mint, tokenProg, ataProg []byte) ([]byte, error) {
	addr, _, err := findProgramAddress(
		[][]byte{owner, tokenProg, mint},
		ataProg,
	)
	return addr, err
}

// findProgramAddress derives a Program Derived Address.
func findProgramAddress(seeds [][]byte, programID []byte) ([]byte, byte, error) {
	for nonce := byte(255); ; nonce-- {
		h := sha256.New()
		for _, s := range seeds {
			h.Write(s)
		}
		h.Write([]byte{nonce})
		h.Write(programID)
		h.Write([]byte("ProgramDerivedAddress"))
		candidate := h.Sum(nil)
		if !isOnCurve(candidate) {
			return candidate, nonce, nil
		}
		if nonce == 0 {
			break
		}
	}
	return nil, 0, fmt.Errorf("could not find valid PDA")
}

// isOnCurve checks if 32 bytes represent a valid ed25519 curve point.
func isOnCurve(point []byte) bool {
	if len(point) != 32 {
		return false
	}

	// Ed25519 prime: p = 2^255 - 19
	p := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 255), big.NewInt(19))

	// Extract y-coordinate (little-endian, clear sign bit).
	yBytes := make([]byte, 32)
	copy(yBytes, point)
	yBytes[31] &= 0x7f
	// Reverse to big-endian for big.Int.
	for i, j := 0, 31; i < j; i, j = i+1, j-1 {
		yBytes[i], yBytes[j] = yBytes[j], yBytes[i]
	}
	y := new(big.Int).SetBytes(yBytes)
	if y.Cmp(p) >= 0 {
		return false
	}

	// d = -121665/121666 mod p
	d := new(big.Int).ModInverse(big.NewInt(121666), p)
	d.Mul(d, big.NewInt(-121665))
	d.Mod(d, p)

	// x² = (y² - 1) / (d·y² + 1) mod p
	y2 := new(big.Int).Mul(y, y)
	y2.Mod(y2, p)

	num := new(big.Int).Sub(y2, big.NewInt(1))
	num.Mod(num, p)

	den := new(big.Int).Mul(d, y2)
	den.Add(den, big.NewInt(1))
	den.Mod(den, p)

	denInv := new(big.Int).ModInverse(den, p)
	if denInv == nil {
		return false
	}
	x2 := new(big.Int).Mul(num, denInv)
	x2.Mod(x2, p)

	if x2.Sign() == 0 {
		return true
	}

	// Euler's criterion: QR iff x2^((p-1)/2) ≡ 1 (mod p)
	exp := new(big.Int).Sub(p, big.NewInt(1))
	exp.Rsh(exp, 1)
	return new(big.Int).Exp(x2, exp, p).Cmp(big.NewInt(1)) == 0
}

// accountMissing checks if a Solana account does not exist.
func accountMissing(ctx context.Context, pubkey []byte) (bool, error) {
	addr := base58Encode(pubkey)
	body, err := solanaRPCCall(ctx, "getAccountInfo", []interface{}{
		addr,
		map[string]string{"encoding": "base64"},
	})
	if err != nil {
		return false, err
	}
	var resp struct {
		Result struct {
			Value interface{} `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return false, err
	}
	return resp.Result.Value == nil, nil
}

// base58Encode encodes bytes to base58 (Bitcoin/Solana alphabet).
func base58Encode(b []byte) string {
	val := new(big.Int).SetBytes(b)
	zero := big.NewInt(0)
	base := big.NewInt(58)
	mod := new(big.Int)

	var result []byte
	for val.Cmp(zero) > 0 {
		val.DivMod(val, base, mod)
		result = append(result, base58Alphabet[mod.Int64()])
	}
	// Leading zeros.
	for _, v := range b {
		if v != 0 {
			break
		}
		result = append(result, '1')
	}
	// Reverse.
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return string(result)
}

// parseTokenAmount converts a decimal string to smallest units given decimals.
func parseTokenAmount(s string, decimals int) (uint64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty amount")
	}
	parts := strings.SplitN(s, ".", 2)
	whole, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid amount: %s", s)
	}

	multiplier := uint64(1)
	for i := 0; i < decimals; i++ {
		multiplier *= 10
	}
	result := whole * multiplier

	if len(parts) == 2 {
		frac := parts[1]
		if len(frac) > decimals {
			frac = frac[:decimals]
		}
		for len(frac) < decimals {
			frac += "0"
		}
		fracVal, err := strconv.ParseUint(frac, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid fractional amount: %s", s)
		}
		result += fracVal
	}

	if result == 0 {
		return 0, fmt.Errorf("amount must be greater than zero")
	}
	return result, nil
}

// getLatestBlockhash fetches a recent blockhash from the Solana RPC.
func getLatestBlockhash(ctx context.Context) (string, error) {
	body, err := solanaRPCCall(ctx, "getLatestBlockhash", []interface{}{
		map[string]string{"commitment": "finalized"},
	})
	if err != nil {
		return "", err
	}
	var resp struct {
		Result struct {
			Value struct {
				Blockhash string `json:"blockhash"`
			} `json:"value"`
		} `json:"result"`
		Error *rpcError `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parsing getLatestBlockhash: %w", err)
	}
	if resp.Error != nil {
		return "", fmt.Errorf("getLatestBlockhash: %s", resp.Error.Message)
	}
	if resp.Result.Value.Blockhash == "" {
		return "", fmt.Errorf("empty blockhash returned")
	}
	return resp.Result.Value.Blockhash, nil
}

// parseSOLAmount converts a decimal SOL string (e.g. "0.5") to lamports.
func parseSOLAmount(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty amount")
	}

	parts := strings.SplitN(s, ".", 2)
	whole, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid amount: %s", s)
	}
	lamports := whole * lamportsPerSOL

	if len(parts) == 2 {
		frac := parts[1]
		if len(frac) > 9 {
			frac = frac[:9]
		}
		for len(frac) < 9 {
			frac += "0"
		}
		fracVal, err := strconv.ParseUint(frac, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid fractional amount: %s", s)
		}
		lamports += fracVal
	}

	if lamports == 0 {
		return 0, fmt.Errorf("amount must be greater than zero")
	}
	return lamports, nil
}

// compactU16 encodes an integer in Solana's compact-u16 format.
func compactU16(n int) []byte {
	if n < 0x80 {
		return []byte{byte(n)}
	}
	if n < 0x4000 {
		return []byte{byte(n&0x7f | 0x80), byte(n >> 7)}
	}
	return []byte{byte(n&0x7f | 0x80), byte((n>>7)&0x7f | 0x80), byte(n >> 14)}
}

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// base58Decode decodes a base58-encoded string (Bitcoin/Solana alphabet).
func base58Decode(s string) ([]byte, error) {
	val := new(big.Int)
	for _, c := range []byte(s) {
		idx := strings.IndexByte(base58Alphabet, c)
		if idx < 0 {
			return nil, fmt.Errorf("invalid base58 character: %c", c)
		}
		val.Mul(val, big.NewInt(58))
		val.Add(val, big.NewInt(int64(idx)))
	}

	result := val.Bytes()

	// Leading '1's represent leading zero bytes.
	numZeros := 0
	for _, c := range []byte(s) {
		if c != '1' {
			break
		}
		numZeros++
	}

	// Pad to ensure Solana keys are 32 bytes.
	needed := numZeros + len(result)
	if needed < 32 {
		needed = 32
	}
	padded := make([]byte, needed)
	copy(padded[needed-len(result):], result)

	return padded, nil
}

func solanaRPCCall(ctx context.Context, method string, params interface{}) ([]byte, error) {
	reqBody, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", solanaRPC, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("solana RPC: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading solana RPC response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("solana RPC status %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}
