package venice

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sky10/sky10/pkg/logging"
	"golang.org/x/sync/singleflight"
)

const (
	defaultAPIURL         = "https://api.venice.ai"
	defaultTopUpUSD       = "10"
	baseChainID           = 8453
	baseSepoliaChainID    = 84532
	authStatement         = "Sign in to Venice AI"
	authHeaderName        = "X-Sign-In-With-X"
	paymentHeaderName     = "X-402-Payment"
	paymentRequiredHeader = "PAYMENT-REQUIRED"
	balanceHeaderName     = "X-Balance-Remaining"
	defaultAuthWindow     = 5 * time.Minute
	usdcDecimals          = 6
	baseUSDCAddress       = "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"
)

var hopByHopHeaders = map[string]struct{}{
	"Connection":          {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}

// WalletSigner captures the OWS operations the proxy needs.
type WalletSigner interface {
	AddressForChain(ctx context.Context, walletName, chain string) (string, error)
	SignMessage(ctx context.Context, walletName, chain, message string) (string, error)
	SignTypedData(ctx context.Context, walletName, chain, typedData string) (string, error)
}

// Config controls the Venice proxy.
type Config struct {
	APIURL     string
	Wallet     string
	TopUpUSD   string
	Timeout    time.Duration
	PathPrefix string
}

// Proxy exposes Venice through a local OpenAI-compatible path while using an
// OWS wallet for auth and x402 top-ups.
type Proxy struct {
	apiURL        *url.URL
	walletName    string
	topUpAmount   *big.Int
	pathPrefix    string
	httpClient    *http.Client
	wallet        WalletSigner
	logger        *slog.Logger
	topUpRequests singleflight.Group
	now           func() time.Time
}

type topUpRequirements struct {
	X402Version int           `json:"x402Version"`
	Accepts     []topUpAccept `json:"accepts"`
}

type topUpAccept struct {
	Network           string `json:"network"`
	Asset             string `json:"asset"`
	Amount            string `json:"amount"`
	MaxAmountRequired string `json:"maxAmountRequired"`
	PayTo             string `json:"payTo"`
}

type paymentPayload struct {
	X402Version int                 `json:"x402Version"`
	Scheme      string              `json:"scheme"`
	Network     string              `json:"network"`
	Payload     paymentInnerPayload `json:"payload"`
}

type paymentInnerPayload struct {
	Signature     string               `json:"signature"`
	Authorization paymentAuthorization `json:"authorization"`
}

type paymentAuthorization struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Value       string `json:"value"`
	ValidAfter  string `json:"validAfter"`
	ValidBefore string `json:"validBefore"`
	Nonce       string `json:"nonce"`
}

type manualTopUpRequest struct {
	AmountUSD string `json:"amountUsd"`
}

// NewProxy builds a Venice proxy with sane defaults.
func NewProxy(cfg Config, wallet WalletSigner, logger *slog.Logger) (*Proxy, error) {
	if cfg.APIURL == "" {
		cfg.APIURL = defaultAPIURL
	}
	if cfg.TopUpUSD == "" {
		cfg.TopUpUSD = defaultTopUpUSD
	}
	if cfg.PathPrefix == "" {
		cfg.PathPrefix = "/venice/v1"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 3 * time.Minute
	}

	apiURL, err := url.Parse(cfg.APIURL)
	if err != nil {
		return nil, fmt.Errorf("parse venice api url: %w", err)
	}
	if apiURL.Scheme == "" || apiURL.Host == "" {
		return nil, fmt.Errorf("venice api url must include scheme and host")
	}

	topUpAmount, err := parseUSDToBaseUnits(cfg.TopUpUSD)
	if err != nil {
		return nil, fmt.Errorf("parse venice top-up amount: %w", err)
	}

	return &Proxy{
		apiURL:      apiURL,
		walletName:  strings.TrimSpace(cfg.Wallet),
		topUpAmount: topUpAmount,
		pathPrefix:  strings.TrimRight(cfg.PathPrefix, "/"),
		httpClient:  &http.Client{Timeout: cfg.Timeout},
		wallet:      wallet,
		logger:      logging.WithComponent(logger, "venice.proxy"),
		now:         time.Now,
	}, nil
}

// Ready reports whether the proxy has enough configuration to serve requests.
func (p *Proxy) Ready() error {
	if p.wallet == nil {
		return fmt.Errorf("OWS wallet client is not available")
	}
	if p.walletName == "" {
		return fmt.Errorf("SKY10_VENICE_WALLET is not set")
	}
	return nil
}

// HandleAPI proxies Venice API calls under the configured path prefix.
func (p *Proxy) HandleAPI(w http.ResponseWriter, r *http.Request) {
	if err := p.Ready(); err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	relativePath := strings.TrimPrefix(r.URL.Path, p.pathPrefix)
	if relativePath == r.URL.Path || relativePath == "" {
		writeJSONError(w, http.StatusNotFound, "unknown venice route")
		return
	}
	if relativePath == "/x402/top-up" {
		p.handleManualTopUp(w, r)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	resp, err := p.forward(r.Context(), relativePath, r.URL.RawQuery, r.Method, cloneHeader(r.Header), body, true)
	if err != nil {
		p.logger.Warn("venice proxy request failed", "path", relativePath, "method", r.Method, "error", err)
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()

	copyResponse(w, resp)
}

func (p *Proxy) handleManualTopUp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "use POST for /venice/v1/x402/top-up")
		return
	}

	amount := new(big.Int).Set(p.topUpAmount)
	if r.Body != nil {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "failed to read request body")
			return
		}
		if len(bytes.TrimSpace(body)) > 0 {
			var req manualTopUpRequest
			if err := json.Unmarshal(body, &req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
				return
			}
			if strings.TrimSpace(req.AmountUSD) != "" {
				parsed, err := parseUSDToBaseUnits(req.AmountUSD)
				if err != nil {
					writeJSONError(w, http.StatusBadRequest, err.Error())
					return
				}
				amount = parsed
			}
		}
	}

	result, err, _ := p.topUpRequests.Do("topup", func() (interface{}, error) {
		return nil, p.topUp(r.Context(), amount)
	})
	_ = result
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":    "ok",
		"amountUsd": formatBaseUnitsUSD(amount),
	})
}

func (p *Proxy) forward(ctx context.Context, path, rawQuery, method string, headers http.Header, body []byte, allowTopUp bool) (*http.Response, error) {
	resp, err := p.doRequest(ctx, path, rawQuery, method, headers, body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusPaymentRequired || !allowTopUp {
		return resp, nil
	}

	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	_, err, _ = p.topUpRequests.Do("topup", func() (interface{}, error) {
		return nil, p.topUp(ctx, p.topUpAmount)
	})
	if err != nil {
		return nil, fmt.Errorf("top-up failed: %w", err)
	}

	return p.doRequest(ctx, path, rawQuery, method, headers, body)
}

func (p *Proxy) doRequest(ctx context.Context, path, rawQuery, method string, headers http.Header, body []byte) (*http.Response, error) {
	upstreamURL := p.apiURL.ResolveReference(&url.URL{Path: "/api/v1" + path, RawQuery: rawQuery})

	req, err := http.NewRequestWithContext(ctx, method, upstreamURL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}
	for name, values := range headers {
		if shouldSkipRequestHeader(name) {
			continue
		}
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	if len(body) > 0 && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	authHeader, err := p.signInHeader(ctx, upstreamURL.String())
	if err != nil {
		return nil, fmt.Errorf("sign venice auth header: %w", err)
	}
	req.Header.Set(authHeaderName, authHeader)

	return p.httpClient.Do(req)
}

func (p *Proxy) signInHeader(ctx context.Context, resourceURL string) (string, error) {
	address, err := p.wallet.AddressForChain(ctx, p.walletName, "base")
	if err != nil {
		return "", err
	}

	now := p.now().UTC()
	nonce, err := randomHex(8)
	if err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	message := buildSIWEMessage(p.apiURL.Host, address, resourceURL, nonce, now, now.Add(defaultAuthWindow), baseChainID)
	signature, err := p.wallet.SignMessage(ctx, p.walletName, "base", message)
	if err != nil {
		return "", err
	}
	signature = ensureHexPrefix(signature)

	payload, err := json.Marshal(map[string]interface{}{
		"address":   address,
		"message":   message,
		"signature": signature,
		"timestamp": now.UnixMilli(),
		"chainId":   baseChainID,
	})
	if err != nil {
		return "", fmt.Errorf("marshal auth header: %w", err)
	}
	return base64.StdEncoding.EncodeToString(payload), nil
}

func (p *Proxy) topUp(ctx context.Context, requestedAmount *big.Int) error {
	requirements, err := p.fetchTopUpRequirements(ctx)
	if err != nil {
		return err
	}
	requirement, minAmount, err := selectTopUpRequirement(requirements.Accepts)
	if err != nil {
		return err
	}

	amount := new(big.Int).Set(requestedAmount)
	if amount.Cmp(minAmount) < 0 {
		p.logger.Info("raising venice top-up to server minimum", "requested_usd", formatBaseUnitsUSD(amount), "minimum_usd", formatBaseUnitsUSD(minAmount))
		amount = minAmount
	}

	network := normalizePaymentNetwork(requirement.Network)
	address, err := p.wallet.AddressForChain(ctx, p.walletName, network)
	if err != nil {
		return fmt.Errorf("resolve %s wallet address: %w", network, err)
	}
	validAfter := p.now().UTC().Add(-10 * time.Minute).Unix()
	validBefore := p.now().UTC().Add(5 * time.Minute).Unix()
	nonce, err := randomHex(32)
	if err != nil {
		return fmt.Errorf("generate payment nonce: %w", err)
	}

	typedData, err := buildTransferTypedData(network, address, requirement.PayTo, amount.String(), requirement.AssetOrDefault(), validAfter, validBefore, nonce)
	if err != nil {
		return err
	}
	signature, err := p.wallet.SignTypedData(ctx, p.walletName, network, typedData)
	if err != nil {
		return fmt.Errorf("sign venice payment: %w", err)
	}
	signature = ensureHexPrefix(signature)

	headerPayload := paymentPayload{
		X402Version: requirements.X402Version,
		Scheme:      "exact",
		Network:     network,
		Payload: paymentInnerPayload{
			Signature: signature,
			Authorization: paymentAuthorization{
				From:        address,
				To:          requirement.PayTo,
				Value:       amount.String(),
				ValidAfter:  fmt.Sprintf("%d", validAfter),
				ValidBefore: fmt.Sprintf("%d", validBefore),
				Nonce:       ensureHexPrefix(nonce),
			},
		},
	}
	encodedHeader, err := encodePaymentHeader(headerPayload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.apiURL.ResolveReference(&url.URL{Path: "/api/v1/x402/top-up"}).String(), nil)
	if err != nil {
		return fmt.Errorf("build top-up request: %w", err)
	}
	req.Header.Set(paymentHeaderName, encodedHeader)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send top-up request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("venice top-up returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	p.logger.Info("venice balance topped up", "network", network, "amount_usd", formatBaseUnitsUSD(amount))
	return nil
}

func (p *Proxy) fetchTopUpRequirements(ctx context.Context) (*topUpRequirements, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.apiURL.ResolveReference(&url.URL{Path: "/api/v1/x402/top-up"}).String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build top-up requirements request: %w", err)
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request top-up requirements: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPaymentRequired {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("expected 402 from venice top-up requirements, got %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed topUpRequirements
	body, _ := io.ReadAll(resp.Body)
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &parsed); err == nil && parsed.X402Version > 0 && len(parsed.Accepts) > 0 {
			return &parsed, nil
		}
	}

	encoded := resp.Header.Get(paymentRequiredHeader)
	if encoded == "" {
		return nil, fmt.Errorf("venice top-up requirements missing %s header", paymentRequiredHeader)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode venice payment requirements: %w", err)
	}
	if err := json.Unmarshal(decoded, &parsed); err != nil {
		return nil, fmt.Errorf("parse venice payment requirements: %w", err)
	}
	if parsed.X402Version == 0 || len(parsed.Accepts) == 0 {
		return nil, fmt.Errorf("venice payment requirements were incomplete")
	}
	return &parsed, nil
}

func selectTopUpRequirement(accepts []topUpAccept) (topUpAccept, *big.Int, error) {
	var fallback topUpAccept
	for _, accept := range accepts {
		if fallback.PayTo == "" {
			fallback = accept
		}
		if normalizePaymentNetwork(accept.Network) == "base" && strings.EqualFold(accept.AssetOrDefault(), baseUSDCAddress) {
			amount, err := parseBaseUnits(accept.RequiredAmount())
			if err != nil {
				return topUpAccept{}, nil, err
			}
			return accept, amount, nil
		}
	}
	if fallback.PayTo == "" {
		return topUpAccept{}, nil, fmt.Errorf("venice top-up returned no payment options")
	}
	amount, err := parseBaseUnits(fallback.RequiredAmount())
	if err != nil {
		return topUpAccept{}, nil, err
	}
	return fallback, amount, nil
}

func (a topUpAccept) RequiredAmount() string {
	if strings.TrimSpace(a.Amount) != "" {
		return strings.TrimSpace(a.Amount)
	}
	return strings.TrimSpace(a.MaxAmountRequired)
}

func (a topUpAccept) AssetOrDefault() string {
	if strings.TrimSpace(a.Asset) != "" {
		return a.Asset
	}
	return baseUSDCAddress
}

func normalizePaymentNetwork(network string) string {
	normalized := strings.ToLower(strings.TrimSpace(network))
	switch normalized {
	case "base-sepolia", "eip155:84532", "84532":
		return "base-sepolia"
	case "base", "eip155:8453", "8453", "":
		return "base"
	default:
		return normalized
	}
}

func chainIDForNetwork(network string) (int, error) {
	switch normalizePaymentNetwork(network) {
	case "base":
		return baseChainID, nil
	case "base-sepolia":
		return baseSepoliaChainID, nil
	default:
		return 0, fmt.Errorf("unsupported venice payment network %q", network)
	}
}

func buildTransferTypedData(network, from, to, amount, asset string, validAfter, validBefore int64, nonce string) (string, error) {
	chainID, err := chainIDForNetwork(network)
	if err != nil {
		return "", err
	}
	typedData, err := json.Marshal(map[string]interface{}{
		"types": map[string]interface{}{
			"EIP712Domain": []map[string]string{
				{"name": "name", "type": "string"},
				{"name": "version", "type": "string"},
				{"name": "chainId", "type": "uint256"},
				{"name": "verifyingContract", "type": "address"},
			},
			"TransferWithAuthorization": []map[string]string{
				{"name": "from", "type": "address"},
				{"name": "to", "type": "address"},
				{"name": "value", "type": "uint256"},
				{"name": "validAfter", "type": "uint256"},
				{"name": "validBefore", "type": "uint256"},
				{"name": "nonce", "type": "bytes32"},
			},
		},
		"primaryType": "TransferWithAuthorization",
		"domain": map[string]interface{}{
			"name":              "USD Coin",
			"version":           "2",
			"chainId":           fmt.Sprintf("%d", chainID),
			"verifyingContract": asset,
		},
		"message": map[string]string{
			"from":        from,
			"to":          to,
			"value":       amount,
			"validAfter":  fmt.Sprintf("%d", validAfter),
			"validBefore": fmt.Sprintf("%d", validBefore),
			"nonce":       ensureHexPrefix(nonce),
		},
	})
	if err != nil {
		return "", fmt.Errorf("marshal payment typed data: %w", err)
	}
	return string(typedData), nil
}

func buildSIWEMessage(domain, address, resourceURL, nonce string, issuedAt, expiresAt time.Time, chainID int) string {
	return fmt.Sprintf(
		"%s wants you to sign in with your Ethereum account:\n%s\n\n%s\n\nURI: %s\nVersion: 1\nChain ID: %d\nNonce: %s\nIssued At: %s\nExpiration Time: %s",
		domain,
		address,
		authStatement,
		resourceURL,
		chainID,
		nonce,
		formatSIWETime(issuedAt),
		formatSIWETime(expiresAt),
	)
}

func formatSIWETime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

func shouldSkipRequestHeader(name string) bool {
	if _, ok := hopByHopHeaders[http.CanonicalHeaderKey(name)]; ok {
		return true
	}
	switch http.CanonicalHeaderKey(name) {
	case "Authorization", authHeaderName, paymentHeaderName, "Host", "Content-Length":
		return true
	default:
		return false
	}
}

func cloneHeader(src http.Header) http.Header {
	dst := make(http.Header, len(src))
	for name, values := range src {
		dst[name] = append([]string(nil), values...)
	}
	return dst
}

func copyResponse(w http.ResponseWriter, resp *http.Response) {
	for name, values := range resp.Header {
		if _, ok := hopByHopHeaders[http.CanonicalHeaderKey(name)]; ok {
			continue
		}
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": message,
	})
}

func parseUSDToBaseUnits(amount string) (*big.Int, error) {
	trimmed := strings.TrimSpace(amount)
	if trimmed == "" {
		return nil, fmt.Errorf("amountUsd is required")
	}

	whole, frac, found := strings.Cut(trimmed, ".")
	if whole == "" {
		whole = "0"
	}
	if strings.HasPrefix(whole, "+") || strings.HasPrefix(whole, "-") {
		return nil, fmt.Errorf("amountUsd must be a positive decimal")
	}
	if !allDigits(whole) {
		return nil, fmt.Errorf("amountUsd must be numeric")
	}
	if found {
		if !allDigits(frac) {
			return nil, fmt.Errorf("amountUsd must be numeric")
		}
		if len(frac) > usdcDecimals {
			return nil, fmt.Errorf("amountUsd supports at most %d decimal places", usdcDecimals)
		}
	} else {
		frac = ""
	}
	frac = frac + strings.Repeat("0", usdcDecimals-len(frac))

	combined := strings.TrimLeft(whole+frac, "0")
	if combined == "" {
		return big.NewInt(0), nil
	}
	value, ok := new(big.Int).SetString(combined, 10)
	if !ok {
		return nil, fmt.Errorf("amountUsd is invalid")
	}
	return value, nil
}

func parseBaseUnits(amount string) (*big.Int, error) {
	trimmed := strings.TrimSpace(amount)
	if trimmed == "" || !allDigits(trimmed) {
		return nil, fmt.Errorf("invalid base-unit amount %q", amount)
	}
	value, ok := new(big.Int).SetString(trimmed, 10)
	if !ok {
		return nil, fmt.Errorf("invalid base-unit amount %q", amount)
	}
	return value, nil
}

func formatBaseUnitsUSD(amount *big.Int) string {
	if amount == nil {
		return "0"
	}
	s := amount.String()
	if len(s) <= usdcDecimals {
		s = strings.Repeat("0", usdcDecimals-len(s)+1) + s
	}
	point := len(s) - usdcDecimals
	whole := s[:point]
	frac := strings.TrimRight(s[point:], "0")
	if frac == "" {
		return whole
	}
	return whole + "." + frac
}

func encodePaymentHeader(payload paymentPayload) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payment payload: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

func ensureHexPrefix(value string) string {
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "0x") || strings.HasPrefix(trimmed, "0X") {
		return trimmed
	}
	return "0x" + trimmed
}

func randomHex(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
