package mpp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	skywallet "github.com/sky10/sky10/pkg/wallet"
)

const (
	paySHAirQualityURL       = "https://airquality.google.gateway-402.com/v1/currentConditions:lookup"
	paySHAirQualityMaxMicros = uint64(2_000)
	paySHAirQualityFixture   = "pay-sh-google-airquality-paid.json"
	paySHAirQualityRequest   = `{"location":{"latitude":37.422131,"longitude":-122.084801},"extraComputations":["LOCAL_AQI"],"languageCode":"en"}`
	paySHSolanaUSDCMainnet   = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
)

// TestPaySHMPPLiveAirQuality makes a real Pay.sh MPP payment on Solana
// mainnet and captures the 402 + paid retry exchange for offline replay.
//
// Run with:
//
//	MPP_LIVE=1 go test ./pkg/payments/mpp -run TestPaySHMPPLiveAirQuality -count=1 -v
//
// This spends real USDC. The test refuses to sign when the quoted charge
// exceeds paySHAirQualityMaxMicros.
func TestPaySHMPPLiveAirQuality(t *testing.T) {
	loadMPPDotEnv()
	if os.Getenv("MPP_LIVE") == "" {
		t.Skip("set MPP_LIVE=1 to run; this spends real USDC")
	}

	walletName := liveWalletName()
	client := skywallet.NewClient()
	if client == nil {
		t.Fatal("ows binary not found via skywallet.NewClient")
	}
	signer := NewOWSSigner(client, walletName)
	if signer == nil {
		t.Fatalf("MPP signer unavailable for wallet %q", walletName)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	rec := &mppLiveRecorder{}
	resp, err := doMPPHTTPRequest(ctx, http.MethodPost, paySHAirQualityURL, []byte(paySHAirQualityRequest), http.Header{
		"Content-Type": []string{"application/json"},
	}, rec)
	if err != nil {
		t.Fatalf("initial request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("initial status = %d, want 402", resp.StatusCode)
	}
	challenges, err := ParseChallenges(resp.Header)
	if err != nil {
		t.Fatalf("ParseChallenges: %v", err)
	}
	if len(challenges) == 0 {
		t.Fatal("Pay.sh did not return an MPP challenge")
	}
	challenge := challenges[0]
	if err := assertPaySHAirQualityCharge(challenge); err != nil {
		t.Fatal(err)
	}

	auth, err := signer.Sign(ctx, challenge)
	if err != nil {
		t.Fatalf("sign MPP payment: %v", err)
	}
	retry, err := doMPPHTTPRequest(ctx, http.MethodPost, paySHAirQualityURL, []byte(paySHAirQualityRequest), http.Header{
		"Content-Type":  []string{"application/json"},
		"Authorization": []string{auth},
	}, rec)
	if err != nil {
		t.Fatalf("paid retry: %v", err)
	}
	defer retry.Body.Close()
	if retry.StatusCode == http.StatusPaymentRequired {
		t.Fatalf("paid retry still returned 402")
	}
	if retry.StatusCode < 200 || retry.StatusCode >= 300 {
		t.Fatalf("paid retry status = %d, body=%s", retry.StatusCode, truncateMPP(rec.exchanges[len(rec.exchanges)-1].ResponseBody, 300))
	}
	receipt, err := ParseReceipt(retry.Header.Get(HeaderPaymentReceipt))
	if err != nil {
		t.Fatalf("parse receipt: %v", err)
	}
	if receipt == nil || strings.TrimSpace(receipt.Reference) == "" {
		t.Fatalf("missing payment receipt: %+v", receipt)
	}

	if err := writePaySHMPPLiveFixture(rec.exchanges); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Logf("paid Pay.sh MPP call settled: status=%d tx=%s", retry.StatusCode, receipt.Reference)
}

func assertPaySHAirQualityCharge(challenge Challenge) error {
	if !strings.EqualFold(challenge.Method, "solana") || !strings.EqualFold(challenge.Intent, "charge") {
		return fmt.Errorf("challenge = %s/%s, want solana/charge", challenge.Method, challenge.Intent)
	}
	req, details, err := challenge.DecodeChargeRequest()
	if err != nil {
		return err
	}
	if req.Currency != paySHSolanaUSDCMainnet {
		return fmt.Errorf("currency = %q, want Solana USDC mint", req.Currency)
	}
	if strings.TrimSpace(req.Recipient) == "" {
		return fmt.Errorf("charge request missing recipient")
	}
	if strings.TrimSpace(details.Network) != "mainnet" && strings.TrimSpace(details.Network) != "mainnet-beta" {
		return fmt.Errorf("network = %q, want Solana mainnet", details.Network)
	}
	if details.FeePayer == nil || !*details.FeePayer || strings.TrimSpace(details.FeePayerKey) == "" {
		return fmt.Errorf("missing Pay.sh fee payer details")
	}
	amount, splitTotal, err := paySHChargeMicros(req, details)
	if err != nil {
		return err
	}
	if splitTotal > amount {
		return fmt.Errorf("Pay.sh MPP splits total %d micro-USDC, more than charge amount %d", splitTotal, amount)
	}
	if amount > paySHAirQualityMaxMicros {
		return fmt.Errorf("Pay.sh MPP quote is %d micro-USDC, cap is %d", amount, paySHAirQualityMaxMicros)
	}
	return nil
}

func paySHChargeMicros(req ChargeRequest, details MethodDetails) (amount uint64, splitTotal uint64, err error) {
	amount, err = parseBaseUnitAmount(req.Amount)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid charge amount: %w", err)
	}
	for _, split := range details.Splits {
		splitAmount, err := parseBaseUnitAmount(split.Amount)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid split amount: %w", err)
		}
		splitTotal += splitAmount
	}
	return amount, splitTotal, nil
}

type mppLiveRecorder struct {
	exchanges []mppLiveExchange
}

type mppLiveExchange struct {
	RequestMethod   string      `json:"request_method"`
	RequestURL      string      `json:"request_url"`
	RequestHeaders  http.Header `json:"request_headers,omitempty"`
	RequestBody     string      `json:"request_body,omitempty"`
	ResponseStatus  int         `json:"response_status"`
	ResponseHeaders http.Header `json:"response_headers,omitempty"`
	ResponseBody    string      `json:"response_body,omitempty"`
}

func doMPPHTTPRequest(ctx context.Context, method, url string, body []byte, headers http.Header, rec *mppLiveRecorder) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	exchange := mppLiveExchange{
		RequestMethod:  method,
		RequestURL:     url,
		RequestHeaders: req.Header.Clone(),
		RequestBody:    string(body),
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		rec.exchanges = append(rec.exchanges, redactMPPExchange(exchange))
		return nil, err
	}
	respBody, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(respBody))
	exchange.ResponseStatus = resp.StatusCode
	exchange.ResponseHeaders = resp.Header.Clone()
	exchange.ResponseBody = string(respBody)
	rec.exchanges = append(rec.exchanges, redactMPPExchange(exchange))
	return resp, readErr
}

type paySHMPPLiveFixture struct {
	Service       string            `json:"service"`
	CapturedAtUTC string            `json:"captured_at_utc"`
	SpendCap      string            `json:"spend_cap"`
	Exchanges     []mppLiveExchange `json:"exchanges"`
}

func writePaySHMPPLiveFixture(exchanges []mppLiveExchange) error {
	fixture := paySHMPPLiveFixture{
		Service:       "pay.sh/solana-foundation/google/airquality",
		CapturedAtUTC: time.Now().UTC().Format(time.RFC3339),
		SpendCap:      "0.002 USDC",
		Exchanges:     exchanges,
	}
	raw, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join("testdata", paySHAirQualityFixture), append(raw, '\n'), 0o644)
}

func redactMPPExchange(exchange mppLiveExchange) mppLiveExchange {
	exchange.RequestHeaders = redactMPPHeaders(exchange.RequestHeaders)
	exchange.ResponseHeaders = redactMPPHeaders(exchange.ResponseHeaders)
	return exchange
}

func redactMPPHeaders(headers http.Header) http.Header {
	out := headers.Clone()
	for key := range out {
		if strings.EqualFold(key, HeaderAuthorization) {
			out[key] = []string{"<REDACTED: real MPP Authorization header sent>"}
		}
	}
	return out
}

func liveWalletName() string {
	for _, key := range []string{"MPP_LIVE_WALLET", "X402_LIVE_WALLET", "OWS_WALLET"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return "default"
}

func loadMPPDotEnv() {
	for _, candidate := range mppDotEnvSearchPaths() {
		f, err := os.Open(candidate)
		if err != nil {
			continue
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			eq := strings.IndexByte(line, '=')
			if eq <= 0 {
				continue
			}
			key := strings.TrimSpace(line[:eq])
			value := line[eq+1:]
			if _, set := os.LookupEnv(key); !set {
				_ = os.Setenv(key, value)
			}
		}
		return
	}
}

func mppDotEnvSearchPaths() []string {
	wd, err := os.Getwd()
	if err != nil {
		return nil
	}
	var out []string
	for dir := wd; ; {
		out = append(out, filepath.Join(dir, ".env"))
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return out
}

func truncateMPP(value string, n int) string {
	if len(value) <= n {
		return value
	}
	return value[:n] + "..."
}
