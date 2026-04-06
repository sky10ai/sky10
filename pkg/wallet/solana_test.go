package wallet

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
)

func TestFormatLamports(t *testing.T) {
	t.Parallel()
	tests := []struct {
		lamports uint64
		want     string
	}{
		{0, "0"},
		{1_000_000_000, "1"},
		{700_130_001, "0.700130001"},
		{1_500_000_000, "1.5"},
		{100_000, "0.0001"},
		{10_000_000_000, "10"},
	}
	for _, tt := range tests {
		got := formatLamports(tt.lamports)
		if got != tt.want {
			t.Errorf("formatLamports(%d) = %q, want %q", tt.lamports, got, tt.want)
		}
	}
}

func TestSolanaBalances_MockRPC(t *testing.T) {
	// Not parallel: mutates global solanaRPC.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		switch req.Method {
		case "getBalance":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      1,
				"result": map[string]interface{}{
					"context": map[string]interface{}{"slot": 1},
					"value":   700130001,
				},
			})
		case "getTokenAccountsByOwner":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      1,
				"result": map[string]interface{}{
					"context": map[string]interface{}{"slot": 1},
					"value":   []interface{}{},
				},
			})
		}
	}))
	defer srv.Close()

	old := solanaRPC
	solanaRPC = srv.URL
	defer func() { solanaRPC = old }()

	result, err := solanaBalances(context.Background(), "6fSWeC5P1icuiW2DfWHxz3rxjjpZXccsNYXJfXYkjaZ4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Address != "6fSWeC5P1icuiW2DfWHxz3rxjjpZXccsNYXJfXYkjaZ4" {
		t.Errorf("address = %q", result.Address)
	}
	if len(result.Tokens) != 2 {
		t.Fatalf("got %d tokens, want 2", len(result.Tokens))
	}
	if result.Tokens[0].Symbol != "SOL" || result.Tokens[0].Balance != "0.700130001" {
		t.Errorf("SOL = %+v", result.Tokens[0])
	}
	if result.Tokens[1].Symbol != "USDC" || result.Tokens[1].Balance != "0" {
		t.Errorf("USDC = %+v", result.Tokens[1])
	}
}

func TestMintSymbol(t *testing.T) {
	t.Parallel()
	if s := mintSymbol(usdcMint); s != "USDC" {
		t.Errorf("USDC mint = %q", s)
	}
	if s := mintSymbol("SomeShortMint"); s != "Some...Mint" {
		t.Errorf("short mint = %q", s)
	}
}

func TestParseSOLAmount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  uint64
		err   bool
	}{
		{"1", 1_000_000_000, false},
		{"0.5", 500_000_000, false},
		{"0.000005", 5_000, false},
		{"0.700130001", 700_130_001, false},
		{"10", 10_000_000_000, false},
		{"0", 0, true},
		{"", 0, true},
		{"abc", 0, true},
	}
	for _, tt := range tests {
		got, err := parseSOLAmount(tt.input)
		if tt.err {
			if err == nil {
				t.Errorf("parseSOLAmount(%q) expected error", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseSOLAmount(%q) error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseSOLAmount(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestParseTokenAmount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input    string
		decimals int
		want     uint64
		err      bool
	}{
		{"1", 6, 1_000_000, false},
		{"1.5", 6, 1_500_000, false},
		{"0.01", 6, 10_000, false},
		{"100", 6, 100_000_000, false},
		{"0", 6, 0, true},
	}
	for _, tt := range tests {
		got, err := parseTokenAmount(tt.input, tt.decimals)
		if tt.err {
			if err == nil {
				t.Errorf("parseTokenAmount(%q, %d) expected error", tt.input, tt.decimals)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseTokenAmount(%q, %d) error: %v", tt.input, tt.decimals, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseTokenAmount(%q, %d) = %d, want %d", tt.input, tt.decimals, got, tt.want)
		}
	}
}

func TestBase58Decode(t *testing.T) {
	t.Parallel()
	b, err := base58Decode(systemProgram)
	if err != nil {
		t.Fatalf("decoding system program: %v", err)
	}
	if len(b) != 32 {
		t.Errorf("system program length = %d, want 32", len(b))
	}
	for i, v := range b {
		if v != 0 {
			t.Errorf("system program byte %d = %d, want 0", i, v)
			break
		}
	}

	addr := "6fSWeC5P1icuiW2DfWHxz3rxjjpZXccsNYXJfXYkjaZ4"
	b, err = base58Decode(addr)
	if err != nil {
		t.Fatalf("decoding address: %v", err)
	}
	if len(b) != 32 {
		t.Errorf("address length = %d, want 32", len(b))
	}
}

func TestBase58RoundTrip(t *testing.T) {
	t.Parallel()
	original := "6fSWeC5P1icuiW2DfWHxz3rxjjpZXccsNYXJfXYkjaZ4"
	decoded, err := base58Decode(original)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	encoded := base58Encode(decoded)
	if encoded != original {
		t.Errorf("round-trip: got %q, want %q", encoded, original)
	}
}

func TestCompactU16(t *testing.T) {
	t.Parallel()
	tests := []struct {
		n    int
		want []byte
	}{
		{0, []byte{0}},
		{3, []byte{3}},
		{127, []byte{127}},
		{128, []byte{0x80, 0x01}},
		{256, []byte{0x80, 0x02}},
	}
	for _, tt := range tests {
		got := compactU16(tt.n)
		if !bytes.Equal(got, tt.want) {
			t.Errorf("compactU16(%d) = %v, want %v", tt.n, got, tt.want)
		}
	}
}

func TestFindProgramAddress(t *testing.T) {
	t.Parallel()
	owner, _ := base58Decode("6fSWeC5P1icuiW2DfWHxz3rxjjpZXccsNYXJfXYkjaZ4")
	mint, _ := base58Decode(usdcMint)
	tokProg, _ := base58Decode(tokenProgram)
	ataProg, _ := base58Decode(ataProgram)

	addr, nonce, err := findProgramAddress(
		[][]byte{owner, tokProg, mint},
		ataProg,
	)
	if err != nil {
		t.Fatalf("findProgramAddress: %v", err)
	}
	if len(addr) != 32 {
		t.Errorf("PDA length = %d, want 32", len(addr))
	}
	if nonce == 0 && addr == nil {
		t.Error("should have found a valid PDA")
	}
	if isOnCurve(addr) {
		t.Error("PDA should not be on the ed25519 curve")
	}
}

// --- Transaction building tests (no OWS required) ---

// mockBlockhashServer returns a test server that handles getLatestBlockhash
// and optionally getAccountInfo (with the given existence).
func mockSolanaRPC(ataExists bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		switch req.Method {
		case "getLatestBlockhash":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": 1,
				"result": map[string]interface{}{
					"context": map[string]interface{}{"slot": 1},
					"value": map[string]interface{}{
						"blockhash":            "4sGjMW1sUnHzSxGspuhpqLDx6wiyjNtZAMdL4VZHirAn",
						"lastValidBlockHeight": 100,
					},
				},
			})
		case "getAccountInfo":
			var value interface{}
			if ataExists {
				value = map[string]interface{}{"data": []string{"", "base64"}, "lamports": 2039280}
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": 1,
				"result": map[string]interface{}{
					"context": map[string]interface{}{"slot": 1},
					"value":   value,
				},
			})
		}
	}))
}

func withMockRPC(t *testing.T, ataExists bool) func() {
	t.Helper()
	srv := mockSolanaRPC(ataExists)
	old := solanaRPC
	solanaRPC = srv.URL
	return func() {
		solanaRPC = old
		srv.Close()
	}
}

const (
	testFrom = "6fSWeC5P1icuiW2DfWHxz3rxjjpZXccsNYXJfXYkjaZ4"
	testTo   = "HwdPHR1gnwujqzhB9Gb7pLw2XHZM5Mxo9pXQHBwhRcg9"
)

func TestBuildSOLTransferTx(t *testing.T) {
	// Not parallel: mutates global solanaRPC.
	cleanup := withMockRPC(t, false)
	defer cleanup()

	txBytes, err := buildSOLTransferTx(context.Background(), testFrom, testTo, 200_000_000)
	if err != nil {
		t.Fatalf("buildSOLTransferTx: %v", err)
	}

	// Unsigned tx envelope: compact(1) + 64 zero bytes + message.
	if txBytes[0] != 1 {
		t.Fatalf("num_signatures = %d, want 1", txBytes[0])
	}
	for i := 1; i <= 64; i++ {
		if txBytes[i] != 0 {
			t.Errorf("signature byte %d = %d, want 0", i-1, txBytes[i])
			break
		}
	}

	msg := txBytes[65:]

	// Header.
	if msg[0] != 1 || msg[1] != 0 || msg[2] != 1 {
		t.Errorf("header = [%d,%d,%d], want [1,0,1]", msg[0], msg[1], msg[2])
	}
	if msg[3] != 3 {
		t.Errorf("num_accounts = %d, want 3", msg[3])
	}

	// Account keys: sender, recipient, system_program.
	fromKey, _ := base58Decode(testFrom)
	toKey, _ := base58Decode(testTo)
	sysKey, _ := base58Decode(systemProgram)
	keys := msg[4:]
	if !bytes.Equal(keys[:32], fromKey) {
		t.Error("account[0] != sender")
	}
	if !bytes.Equal(keys[32:64], toKey) {
		t.Error("account[1] != recipient")
	}
	if !bytes.Equal(keys[64:96], sysKey) {
		t.Error("account[2] != system_program")
	}

	// Skip blockhash (32 bytes) -> instructions.
	instr := keys[96+32:]
	if instr[0] != 1 { // 1 instruction
		t.Fatalf("num_instructions = %d, want 1", instr[0])
	}
	instr = instr[1:]
	if instr[0] != 2 { // program_id_index = system_program
		t.Errorf("program_id_index = %d, want 2", instr[0])
	}
	if instr[1] != 2 { // 2 account indices
		t.Errorf("num_acct_indices = %d, want 2", instr[1])
	}
	if instr[2] != 0 || instr[3] != 1 { // [from, to]
		t.Errorf("acct_indices = [%d,%d], want [0,1]", instr[2], instr[3])
	}
	if instr[4] != 12 { // data length
		t.Errorf("data_len = %d, want 12", instr[4])
	}

	// Instruction data: u32 LE = 2 (Transfer), u64 LE = 200_000_000.
	data := instr[5:]
	if binary.LittleEndian.Uint32(data[:4]) != 2 {
		t.Errorf("system instruction = %d, want 2", binary.LittleEndian.Uint32(data[:4]))
	}
	if binary.LittleEndian.Uint64(data[4:12]) != 200_000_000 {
		t.Errorf("lamports = %d, want 200000000", binary.LittleEndian.Uint64(data[4:12]))
	}
}

func TestBuildSPLTransferTx_TransferOnly(t *testing.T) {
	// Not parallel: mutates global solanaRPC.
	cleanup := withMockRPC(t, true) // recipient ATA exists
	defer cleanup()

	txBytes, err := buildSPLTransferTx(context.Background(), testFrom, testTo, usdcMint, 1_230_000)
	if err != nil {
		t.Fatalf("buildSPLTransferTx: %v", err)
	}

	msg := txBytes[65:]

	// Transfer-only: header [1, 0, 1], 4 accounts.
	if msg[0] != 1 || msg[1] != 0 || msg[2] != 1 {
		t.Errorf("header = [%d,%d,%d], want [1,0,1]", msg[0], msg[1], msg[2])
	}
	if msg[3] != 4 {
		t.Fatalf("num_accounts = %d, want 4", msg[3])
	}

	// Last account should be token program.
	tokProgKey, _ := base58Decode(tokenProgram)
	lastKey := msg[4+3*32 : 4+4*32]
	if !bytes.Equal(lastKey, tokProgKey) {
		t.Error("account[3] != token_program")
	}

	// Skip keys (4*32) + blockhash (32) -> instructions.
	instr := msg[4+4*32+32:]
	if instr[0] != 1 { // 1 instruction
		t.Fatalf("num_instructions = %d, want 1", instr[0])
	}
	instr = instr[1:]
	if instr[0] != 3 { // program_id_index = token_program (index 3)
		t.Errorf("program_id_index = %d, want 3", instr[0])
	}
	if instr[1] != 3 { // 3 account indices
		t.Errorf("num_acct_indices = %d, want 3", instr[1])
	}
	// Accounts: source(1), dest(2), owner(0).
	if instr[2] != 1 || instr[3] != 2 || instr[4] != 0 {
		t.Errorf("acct_indices = [%d,%d,%d], want [1,2,0]", instr[2], instr[3], instr[4])
	}
	if instr[5] != 9 { // data length
		t.Errorf("data_len = %d, want 9", instr[5])
	}
	if instr[6] != 3 { // SPL Transfer = 3
		t.Errorf("spl_instruction = %d, want 3", instr[6])
	}
	amount := binary.LittleEndian.Uint64(instr[7:15])
	if amount != 1_230_000 {
		t.Errorf("amount = %d, want 1230000", amount)
	}
}

func TestBuildSPLTransferTx_WithCreateATA(t *testing.T) {
	// Not parallel: mutates global solanaRPC.
	cleanup := withMockRPC(t, false) // recipient ATA does NOT exist
	defer cleanup()

	txBytes, err := buildSPLTransferTx(context.Background(), testFrom, testTo, usdcMint, 500_000)
	if err != nil {
		t.Fatalf("buildSPLTransferTx with CreateATA: %v", err)
	}

	msg := txBytes[65:]

	// CreateATA + Transfer: header [1, 0, 5], 8 accounts.
	if msg[0] != 1 || msg[1] != 0 || msg[2] != 5 {
		t.Errorf("header = [%d,%d,%d], want [1,0,5]", msg[0], msg[1], msg[2])
	}
	if msg[3] != 8 {
		t.Fatalf("num_accounts = %d, want 8", msg[3])
	}

	// Skip keys (8*32) + blockhash (32) -> instructions.
	instr := msg[4+8*32+32:]
	if instr[0] != 2 { // 2 instructions
		t.Fatalf("num_instructions = %d, want 2", instr[0])
	}
	instr = instr[1:]

	// Instruction 1: CreateATA (program_id_index = 7).
	if instr[0] != 7 {
		t.Errorf("createATA program_id_index = %d, want 7", instr[0])
	}
	if instr[1] != 6 { // 6 accounts
		t.Errorf("createATA num_accounts = %d, want 6", instr[1])
	}
	wantIdx := []byte{0, 1, 3, 4, 5, 6}
	for i, want := range wantIdx {
		if instr[2+i] != want {
			t.Errorf("createATA acct[%d] = %d, want %d", i, instr[2+i], want)
		}
	}
	if instr[8] != 0 { // no data
		t.Errorf("createATA data_len = %d, want 0", instr[8])
	}

	// Instruction 2: Transfer (program_id_index = 6).
	instr = instr[9:]
	if instr[0] != 6 {
		t.Errorf("transfer program_id_index = %d, want 6", instr[0])
	}
}

// --- OWS integration tests (skipped when OWS is not installed) ---

func requireOWS(t *testing.T) string {
	t.Helper()
	bin, err := BinPath()
	if err != nil {
		t.Skip("ows: cannot determine bin path")
	}
	if _, err := os.Stat(bin); err != nil {
		bin, err = exec.LookPath("ows")
		if err != nil {
			t.Skip("ows: not installed, skipping integration test")
		}
	}
	return bin
}

func TestOWS_SignSOLTransfer(t *testing.T) {
	bin := requireOWS(t)

	txBytes, err := buildSOLTransferTx(context.Background(), testFrom, testTo, 100_000)
	if err != nil {
		t.Fatalf("building SOL tx: %v", err)
	}

	cmd := exec.Command(bin, "sign", "tx",
		"--chain", "solana",
		"--wallet", "default",
		"--tx", hex.EncodeToString(txBytes),
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("ows sign tx (SOL): %v\nstderr: %s", err, stderr.String())
	}
	signed := bytes.TrimSpace(stdout.Bytes())
	if len(signed) == 0 {
		t.Fatal("ows sign tx returned empty output")
	}
	t.Logf("signed SOL tx: %s", signed[:min(len(signed), 40)])
}

func TestOWS_SignUSDCTransfer(t *testing.T) {
	bin := requireOWS(t)

	txBytes, err := buildSPLTransferTx(context.Background(), testFrom, testTo, usdcMint, 100_000)
	if err != nil {
		t.Fatalf("building USDC tx: %v", err)
	}

	cmd := exec.Command(bin, "sign", "tx",
		"--chain", "solana",
		"--wallet", "default",
		"--tx", hex.EncodeToString(txBytes),
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("ows sign tx (USDC): %v\nstderr: %s", err, stderr.String())
	}
	signed := bytes.TrimSpace(stdout.Bytes())
	if len(signed) == 0 {
		t.Fatal("ows sign tx returned empty output")
	}
	t.Logf("signed USDC tx: %s", signed[:min(len(signed), 40)])
}
