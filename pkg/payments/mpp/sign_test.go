package mpp

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	skywallet "github.com/sky10/sky10/pkg/wallet"
)

const (
	testSolanaFrom      = "6fSWeC5P1icuiW2DfWHxz3rxjjpZXccsNYXJfXYkjaZ4"
	testSolanaRecipient = "HwdPHR1gnwujqzhB9Gb7pLw2XHZM5Mxo9pXQHBwhRcg9"
	testSolanaFeePayer  = "9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM"
	testBlockhash       = "4sGjMW1sUnHzSxGspuhpqLDx6wiyjNtZAMdL4VZHirAn"
)

func TestOWSSignerSolanaChargeAuthorization(t *testing.T) {
	t.Parallel()
	challenge := testChargeChallenge(t, map[string]any{
		"amount":     "1000",
		"currency":   "USDC",
		"recipient":  testSolanaRecipient,
		"externalId": "order-123",
		"methodDetails": map[string]any{
			"network":         "mainnet-beta",
			"decimals":        6,
			"feePayer":        true,
			"feePayerKey":     testSolanaFeePayer,
			"recentBlockhash": testBlockhash,
		},
	})

	unsignedHex := testUnsignedTxHex(2)
	signature := bytesOf(0x11, 64)
	signer := &OWSSigner{
		WalletName: "default",
		AddressForChain: func(_ context.Context, walletName, chain string) (string, error) {
			if walletName != "default" || chain != skywallet.ChainSolana {
				t.Fatalf("AddressForChain(%q, %q), want default/solana", walletName, chain)
			}
			return testSolanaFrom, nil
		},
		BuildTx: func(_ context.Context, opts skywallet.SolanaPaymentTxOptions) (*skywallet.SolanaPaymentTx, error) {
			if opts.From != testSolanaFrom || opts.Recipient != testSolanaRecipient || opts.FeePayer != testSolanaFeePayer {
				t.Fatalf("payment parties = %+v", opts)
			}
			if opts.Mint != "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v" || opts.TokenProgram != skywallet.SolanaTokenProgram {
				t.Fatalf("mint/program = %q/%q", opts.Mint, opts.TokenProgram)
			}
			if opts.Amount != 1000 || opts.Decimals != 6 || opts.Memo != "order-123" || opts.RecentBlockhash != testBlockhash {
				t.Fatalf("payment opts = %+v", opts)
			}
			return &skywallet.SolanaPaymentTx{
				FullUnsignedHex: unsignedHex,
				MessageHex:      "cafebabe",
				SignerSlot:      1,
			}, nil
		},
		SignTx: func(_ context.Context, walletName, chain, txHex string) (string, error) {
			if walletName != "default" || chain != skywallet.ChainSolana || txHex != unsignedHex {
				t.Fatalf("SignTx(%q, %q, %q)", walletName, chain, txHex)
			}
			return hex.EncodeToString(signature), nil
		},
	}

	header, err := signer.Sign(context.Background(), challenge)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	credential := decodeCredential(t, header)
	if credential.Challenge.ID != "challenge-1" || credential.Source != "did:pkh:solana:mainnet-beta:"+testSolanaFrom {
		t.Fatalf("credential metadata = %+v", credential)
	}
	signed, err := base64.StdEncoding.DecodeString(credential.Payload.Transaction)
	if err != nil {
		t.Fatalf("decode tx: %v", err)
	}
	if got := signed[1+64 : 1+128]; !strings.EqualFold(hex.EncodeToString(got), hex.EncodeToString(signature)) {
		t.Fatalf("signature slot 1 = %x, want %x", got, signature)
	}
}

func TestOWSSignerSolanaChargeUsesCustomTokenProgram(t *testing.T) {
	t.Parallel()
	const xoMint = "xoUSDq85Rjsb6SbUwJyreFgeWQvxdkT7R3c3g7s6p5Y"
	challenge := testChargeChallenge(t, map[string]any{
		"amount":    "2500",
		"currency":  xoMint,
		"recipient": testSolanaRecipient,
		"methodDetails": map[string]any{
			"network":      "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp",
			"decimals":     6,
			"tokenProgram": skywallet.SolanaToken2022Program,
		},
	})

	signer := &OWSSigner{
		WalletName: "default",
		AddressForChain: func(context.Context, string, string) (string, error) {
			return testSolanaFrom, nil
		},
		BuildTx: func(_ context.Context, opts skywallet.SolanaPaymentTxOptions) (*skywallet.SolanaPaymentTx, error) {
			if opts.Mint != xoMint || opts.TokenProgram != skywallet.SolanaToken2022Program || opts.Amount != 2500 {
				t.Fatalf("payment opts = %+v", opts)
			}
			return &skywallet.SolanaPaymentTx{
				FullUnsignedHex: testUnsignedTxHex(1),
				MessageHex:      "cafebabe",
				SignerSlot:      0,
			}, nil
		},
		SignTx: func(context.Context, string, string, string) (string, error) {
			return hex.EncodeToString(bytesOf(0x22, 64)), nil
		},
	}

	if _, err := signer.Sign(context.Background(), challenge); err != nil {
		t.Fatalf("Sign: %v", err)
	}
}

func TestOWSSignerSignsPaySHMPPFixture(t *testing.T) {
	t.Parallel()
	fixture := loadPaySHMPPFixture(t, "pay-sh-google-airquality-402.json")
	challenges, err := ParseChallenges(fixture.Response.Headers)
	if err != nil {
		t.Fatalf("ParseChallenges: %v", err)
	}
	if len(challenges) != 1 {
		t.Fatalf("challenges = %d, want 1", len(challenges))
	}

	signature := bytesOf(0x33, 64)
	signer := &OWSSigner{
		WalletName: "default",
		AddressForChain: func(context.Context, string, string) (string, error) {
			return testSolanaFrom, nil
		},
		BuildTx: func(ctx context.Context, opts skywallet.SolanaPaymentTxOptions) (*skywallet.SolanaPaymentTx, error) {
			if opts.Amount != 1000 || opts.Mint != "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v" {
				t.Fatalf("payment opts = %+v", opts)
			}
			if opts.FeePayer == "" || opts.RecentBlockhash == "" {
				t.Fatalf("fixture did not supply fee payer/blockhash: %+v", opts)
			}
			if len(opts.Splits) != 2 || opts.Splits[0].Amount != 250 || opts.Splits[1].Amount != 1 {
				t.Fatalf("splits = %+v", opts.Splits)
			}
			return skywallet.BuildSolanaPaymentTx(ctx, opts)
		},
		SignTx: func(context.Context, string, string, string) (string, error) {
			return hex.EncodeToString(signature), nil
		},
	}

	header, err := signer.Sign(context.Background(), challenges[0])
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	credential := decodeCredential(t, header)
	if credential.Source != "did:pkh:solana:mainnet-beta:"+testSolanaFrom {
		t.Fatalf("source = %q", credential.Source)
	}
	signed, err := base64.StdEncoding.DecodeString(credential.Payload.Transaction)
	if err != nil {
		t.Fatalf("decode tx: %v", err)
	}
	if len(signed) == 0 {
		t.Fatal("signed transaction empty")
	}
}

func testChargeChallenge(t *testing.T, request map[string]any) Challenge {
	t.Helper()
	encoded, err := base64URLEncodeJSON(request)
	if err != nil {
		t.Fatal(err)
	}
	return Challenge{
		ID:      "challenge-1",
		Realm:   "pay",
		Method:  "solana",
		Intent:  "charge",
		Request: encoded,
	}
}

type decodedCredential struct {
	Challenge ChallengeEcho                `json:"challenge"`
	Source    string                       `json:"source"`
	Payload   CredentialPayloadTransaction `json:"payload"`
}

func decodeCredential(t *testing.T, header string) decodedCredential {
	t.Helper()
	const prefix = "Payment "
	if !strings.HasPrefix(header, prefix) {
		t.Fatalf("authorization = %q, want Payment prefix", header)
	}
	raw, err := base64.RawURLEncoding.DecodeString(header[len(prefix):])
	if err != nil {
		t.Fatalf("decode authorization: %v", err)
	}
	var credential decodedCredential
	if err := json.Unmarshal(raw, &credential); err != nil {
		t.Fatalf("unmarshal credential: %v", err)
	}
	return credential
}

func testUnsignedTxHex(signatureSlots int) string {
	raw := []byte{byte(signatureSlots)}
	raw = append(raw, make([]byte, 64*signatureSlots)...)
	raw = append(raw, 0xca, 0xfe, 0xba, 0xbe)
	return hex.EncodeToString(raw)
}

func bytesOf(value byte, count int) []byte {
	out := make([]byte, count)
	for i := range out {
		out[i] = value
	}
	return out
}
