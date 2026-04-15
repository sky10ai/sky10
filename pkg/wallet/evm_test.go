package wallet

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	btc_ecdsa "github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"golang.org/x/crypto/sha3"
)

const (
	testBasePrivateKeyHex = "4c0883a69102937d6231471b5dbb6204fe512961708279de1cf7636f8b1f6f5d"
	testBaseFromAddress   = "0x16c4876da067d976721b9cdf0de756cd6f59ca89"
	testBaseETHRecipient  = "0x1111111111111111111111111111111111111111"
	testBaseUSDCRecipient = "0x2222222222222222222222222222222222222222"
	testBaseETHAmount     = "0.0000000000012345"
	testBaseUSDCAmount    = "1.234567"

	testBaseETHUnsignedHex = "02ea82210507830f4240832625a08252089411111111111111111111111111111111111111118312d64480c0"
	testBaseETHSignedHex   = "02f86d82210507830f4240832625a08252089411111111111111111111111111111111111111118312d64480c080a061c479d58f9c18cfc59d0c70d361e9329e0bec38a025d9abeec5b7fbc142bc84a030baa914cac0369cf359a4975492093fbee72a7da79ea669bb3bfbb6420bcb06"

	testBaseUSDCUnsignedHex = "02f86c82210507830f4240832625a082fd2094833589fcd6edb6e08f4c7c32d4f71b54bda0291380b844a9059cbb0000000000000000000000002222222222222222222222222222222222222222000000000000000000000000000000000000000000000000000000000012d687c0"
	testBaseUSDCSignedHex   = "02f8af82210507830f4240832625a082fd2094833589fcd6edb6e08f4c7c32d4f71b54bda0291380b844a9059cbb0000000000000000000000002222222222222222222222222222222222222222000000000000000000000000000000000000000000000000000000000012d687c080a0128014b0d2620bcfaf7e56adcddaeb9b6bc240dc91af1e68a430d496a9eda181a0091d1ae0e6f407e037ddb3773f33a1382c8c76fcca9ae59c074c0dfdd96f59c5"
)

func TestParseEVMAmount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		decimals int
		want     string
		wantErr  bool
	}{
		{name: "whole eth", input: "2", decimals: 18, want: "2000000000000000000"},
		{name: "fractional eth", input: "1.5", decimals: 18, want: "1500000000000000000"},
		{name: "leading dot", input: ".25", decimals: 6, want: "250000"},
		{name: "zero", input: "0", decimals: 6, want: "0"},
		{name: "too many decimals", input: "1.0000001", decimals: 6, wantErr: true},
		{name: "invalid chars", input: "1e6", decimals: 6, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseEVMAmount(tt.input, tt.decimals)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseEVMAmount(%q, %d) unexpectedly succeeded", tt.input, tt.decimals)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseEVMAmount(%q, %d): %v", tt.input, tt.decimals, err)
			}
			if got.String() != tt.want {
				t.Fatalf("parseEVMAmount(%q, %d) = %s, want %s", tt.input, tt.decimals, got.String(), tt.want)
			}
		})
	}
}

func TestEncodeERC20Transfer(t *testing.T) {
	t.Parallel()

	got, err := encodeERC20Transfer("0x00000000000000000000000000000000000000ff", big.NewInt(1234567))
	if err != nil {
		t.Fatalf("encodeERC20Transfer: %v", err)
	}

	const want = "a9059cbb00000000000000000000000000000000000000000000000000000000000000ff000000000000000000000000000000000000000000000000000000000012d687"
	if hex.EncodeToString(got) != want {
		t.Fatalf("encodeERC20Transfer() = %q, want %q", hex.EncodeToString(got), want)
	}
}

func TestEncodeEIP1559UnsignedTx(t *testing.T) {
	t.Parallel()

	got, err := encodeEIP1559UnsignedTx(
		baseChainID,
		7,
		big.NewInt(1_000_000),
		big.NewInt(2_500_000),
		21_000,
		"0x1111111111111111111111111111111111111111",
		big.NewInt(1_234_500),
		nil,
	)
	if err != nil {
		t.Fatalf("encodeEIP1559UnsignedTx: %v", err)
	}

	const want = "02ea82210507830f4240832625a08252089411111111111111111111111111111111111111118312d64480c0"
	if hex.EncodeToString(got) != want {
		t.Fatalf("encodeEIP1559UnsignedTx() = %q, want %q", hex.EncodeToString(got), want)
	}
}

func TestBuildBaseTransferTxETH(t *testing.T) {
	server, calls := newBaseRPCTestServer(t)
	defer server.Close()

	old := baseRPC
	baseRPC = server.URL
	defer func() { baseRPC = old }()

	got, err := buildBaseTransferTx(context.Background(), testBaseFromAddress, testBaseETHRecipient, testBaseETHAmount, "ETH")
	if err != nil {
		t.Fatalf("buildBaseTransferTx(ETH): %v", err)
	}

	if hex.EncodeToString(got) != testBaseETHUnsignedHex {
		t.Fatalf("buildBaseTransferTx(ETH) = %q, want %q", hex.EncodeToString(got), testBaseETHUnsignedHex)
	}

	wantCalls := []string{
		"eth_getTransactionCount",
		"eth_gasPrice",
		"eth_maxPriorityFeePerGas",
		"eth_getBlockByNumber",
	}
	if !reflect.DeepEqual(*calls, wantCalls) {
		t.Fatalf("ETH RPC methods = %v, want %v", *calls, wantCalls)
	}
}

func TestBuildBaseTransferTxUSDC(t *testing.T) {
	server, calls := newBaseRPCTestServer(t)
	defer server.Close()

	old := baseRPC
	baseRPC = server.URL
	defer func() { baseRPC = old }()

	got, err := buildBaseTransferTx(context.Background(), testBaseFromAddress, testBaseUSDCRecipient, testBaseUSDCAmount, "USDC")
	if err != nil {
		t.Fatalf("buildBaseTransferTx(USDC): %v", err)
	}

	if hex.EncodeToString(got) != testBaseUSDCUnsignedHex {
		t.Fatalf("buildBaseTransferTx(USDC) = %q, want %q", hex.EncodeToString(got), testBaseUSDCUnsignedHex)
	}

	wantCalls := []string{
		"eth_getTransactionCount",
		"eth_gasPrice",
		"eth_maxPriorityFeePerGas",
		"eth_getBlockByNumber",
		"eth_estimateGas",
	}
	if !reflect.DeepEqual(*calls, wantCalls) {
		t.Fatalf("USDC RPC methods = %v, want %v", *calls, wantCalls)
	}
}

func TestSignBaseETHTxFixture(t *testing.T) {
	t.Parallel()

	got, err := signTypedTx(testBasePrivateKeyHex, mustDecodeHex(t, testBaseETHUnsignedHex))
	if err != nil {
		t.Fatalf("signTypedTx(ETH): %v", err)
	}
	if hex.EncodeToString(got) != testBaseETHSignedHex {
		t.Fatalf("signed ETH tx = %q, want %q", hex.EncodeToString(got), testBaseETHSignedHex)
	}
}

func TestSignBaseUSDCTxFixture(t *testing.T) {
	t.Parallel()

	got, err := signTypedTx(testBasePrivateKeyHex, mustDecodeHex(t, testBaseUSDCUnsignedHex))
	if err != nil {
		t.Fatalf("signTypedTx(USDC): %v", err)
	}
	if hex.EncodeToString(got) != testBaseUSDCSignedHex {
		t.Fatalf("signed USDC tx = %q, want %q", hex.EncodeToString(got), testBaseUSDCSignedHex)
	}
}

func TestBaseSigningKeyAddressFixture(t *testing.T) {
	t.Parallel()

	priv := mustTestPrivateKey(t, testBasePrivateKeyHex)
	got := ethAddressFromPubKey(priv.PubKey())
	if got != testBaseFromAddress {
		t.Fatalf("ethAddressFromPubKey() = %q, want %q", got, testBaseFromAddress)
	}
}

func TestParseBroadcastResult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "transaction_hash", body: `{"transaction_hash":"0xabc"}`, want: "0xabc"},
		{name: "txHash", body: `{"txHash":"0xdef"}`, want: "0xdef"},
		{name: "tx_response", body: `{"tx_response":{"txhash":"0x123"}}`, want: "0x123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := &PayResult{}
			parseBroadcastResult([]byte(tt.body), result)
			if result.TxHash != tt.want {
				t.Fatalf("parseBroadcastResult(%s) tx hash = %q, want %q", tt.name, result.TxHash, tt.want)
			}
		})
	}
}

func newBaseRPCTestServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()

	calls := make([]string, 0, 5)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode base RPC request: %v", err)
		}

		calls = append(calls, req.Method)

		var result interface{}
		switch req.Method {
		case "eth_getTransactionCount":
			result = "0x7"
		case "eth_gasPrice":
			result = "0x2625a0"
		case "eth_maxPriorityFeePerGas":
			result = "0xf4240"
		case "eth_getBlockByNumber":
			result = map[string]string{"baseFeePerGas": "0x7a120"}
		case "eth_estimateGas":
			assertEstimateGasParams(t, req.Params)
			result = "0xd2f0"
		default:
			t.Fatalf("unexpected base RPC method: %s", req.Method)
		}

		if err := json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  result,
		}); err != nil {
			t.Fatalf("encode base RPC response: %v", err)
		}
	}))

	return server, &calls
}

func assertEstimateGasParams(t *testing.T, raw json.RawMessage) {
	t.Helper()

	var params []map[string]string
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatalf("decode eth_estimateGas params: %v", err)
	}
	if len(params) != 1 {
		t.Fatalf("eth_estimateGas params len = %d, want 1", len(params))
	}

	arg := params[0]
	if arg["from"] != testBaseFromAddress {
		t.Fatalf("eth_estimateGas from = %q, want %q", arg["from"], testBaseFromAddress)
	}
	if arg["to"] != "0x833589fcd6edb6e08f4c7c32d4f71b54bda02913" {
		t.Fatalf("eth_estimateGas to = %q, want base USDC contract", arg["to"])
	}
	if arg["value"] != "0x0" {
		t.Fatalf("eth_estimateGas value = %q, want 0x0", arg["value"])
	}
	const wantData = "0xa9059cbb0000000000000000000000002222222222222222222222222222222222222222000000000000000000000000000000000000000000000000000000000012d687"
	if arg["data"] != wantData {
		t.Fatalf("eth_estimateGas data = %q, want %q", arg["data"], wantData)
	}
}

func mustDecodeHex(t *testing.T, value string) []byte {
	t.Helper()

	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatalf("hex.DecodeString(%q): %v", value, err)
	}
	return decoded
}

func mustTestPrivateKey(t *testing.T, privHex string) *btcec.PrivateKey {
	t.Helper()

	privBytes := mustDecodeHex(t, privHex)
	priv, _ := btcec.PrivKeyFromBytes(privBytes)
	return priv
}

func signTypedTx(privHex string, unsigned []byte) ([]byte, error) {
	priv, _ := btcec.PrivKeyFromBytes(mustDecodeHexNoTest(privHex))
	digest := keccak256(unsigned)
	compact := btc_ecdsa.SignCompact(priv, digest, false)
	recoveryID := compact[0] - 27
	r := compact[1:33]
	s := compact[33:65]
	return encodeSignedTypedTx(unsigned, recoveryID, r, s)
}

func encodeSignedTypedTx(unsigned []byte, yParity byte, r, s []byte) ([]byte, error) {
	if len(unsigned) == 0 {
		return nil, errInvalidRLP
	}

	typeByte := unsigned[0]
	rlpData := unsigned[1:]
	payloadOffset, payloadLength, err := decodeRLPList(rlpData)
	if err != nil {
		return nil, err
	}
	if len(rlpData) < payloadOffset+payloadLength {
		return nil, errInvalidRLP
	}

	items, err := splitRLPItems(rlpData[payloadOffset : payloadOffset+payloadLength])
	if err != nil {
		return nil, err
	}
	items = append(items,
		rlpEncodeUint64(uint64(yParity)),
		rlpEncodeBytes(trimLeadingZeroBytes(r)),
		rlpEncodeBytes(trimLeadingZeroBytes(s)),
	)

	signed := append([]byte{typeByte}, rlpEncodeList(items...)...)
	return signed, nil
}

func decodeRLPList(b []byte) (int, int, error) {
	if len(b) == 0 {
		return 0, 0, errInvalidRLP
	}
	prefix := b[0]
	switch {
	case prefix >= 0xc0 && prefix <= 0xf7:
		return 1, int(prefix - 0xc0), nil
	case prefix > 0xf7:
		lenOfLen := int(prefix - 0xf7)
		if len(b) < 1+lenOfLen {
			return 0, 0, errInvalidRLP
		}
		payloadLen := 0
		for _, x := range b[1 : 1+lenOfLen] {
			payloadLen = (payloadLen << 8) | int(x)
		}
		return 1 + lenOfLen, payloadLen, nil
	default:
		return 0, 0, errInvalidRLP
	}
}

func splitRLPItems(b []byte) ([][]byte, error) {
	items := make([][]byte, 0)
	for len(b) > 0 {
		itemLen, err := rlpItemLength(b)
		if err != nil {
			return nil, err
		}
		items = append(items, append([]byte(nil), b[:itemLen]...))
		b = b[itemLen:]
	}
	return items, nil
}

func rlpItemLength(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, errInvalidRLP
	}
	prefix := b[0]
	switch {
	case prefix <= 0x7f:
		return 1, nil
	case prefix <= 0xb7:
		length := int(prefix - 0x80)
		if len(b) < 1+length {
			return 0, errInvalidRLP
		}
		return 1 + length, nil
	case prefix <= 0xbf:
		lenOfLen := int(prefix - 0xb7)
		if len(b) < 1+lenOfLen {
			return 0, errInvalidRLP
		}
		length := 0
		for _, x := range b[1 : 1+lenOfLen] {
			length = (length << 8) | int(x)
		}
		if len(b) < 1+lenOfLen+length {
			return 0, errInvalidRLP
		}
		return 1 + lenOfLen + length, nil
	case prefix <= 0xf7:
		length := int(prefix - 0xc0)
		if len(b) < 1+length {
			return 0, errInvalidRLP
		}
		return 1 + length, nil
	default:
		lenOfLen := int(prefix - 0xf7)
		if len(b) < 1+lenOfLen {
			return 0, errInvalidRLP
		}
		length := 0
		for _, x := range b[1 : 1+lenOfLen] {
			length = (length << 8) | int(x)
		}
		if len(b) < 1+lenOfLen+length {
			return 0, errInvalidRLP
		}
		return 1 + lenOfLen + length, nil
	}
}

func trimLeadingZeroBytes(b []byte) []byte {
	index := 0
	for index < len(b) && b[index] == 0 {
		index++
	}
	return b[index:]
}

func ethAddressFromPubKey(pub *btcec.PublicKey) string {
	uncompressed := pub.SerializeUncompressed()
	sum := keccak256(uncompressed[1:])
	return "0x" + hex.EncodeToString(sum[12:])
}

func keccak256(b []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(b)
	return h.Sum(nil)
}

func mustDecodeHexNoTest(value string) []byte {
	decoded, err := hex.DecodeString(value)
	if err != nil {
		panic(err)
	}
	return decoded
}

var errInvalidRLP = &rlpError{"invalid rlp"}

type rlpError struct {
	message string
}

func (e *rlpError) Error() string { return e.message }
