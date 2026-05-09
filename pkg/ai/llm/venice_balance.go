package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sky10/sky10/pkg/x402/siwx"
)

// VeniceBalanceAddressResolver resolves the wallet address used for a Venice
// x402 account balance lookup.
type VeniceBalanceAddressResolver interface {
	AddressForChain(ctx context.Context, walletName, chain string) (string, error)
}

// VeniceBalanceSignerFactory returns a SIWX signer for a wallet name.
type VeniceBalanceSignerFactory func(walletName string) siwx.Signer

// VeniceBalanceClient queries Venice's own x402 balance endpoint for the
// wallet configured on a Venice AI connection.
type VeniceBalanceClient struct {
	AddressResolver VeniceBalanceAddressResolver
	Signer          VeniceBalanceSignerFactory
	HTTP            *http.Client
	Now             func() time.Time
}

// VeniceBalance implements VeniceBalanceProvider.
func (c *VeniceBalanceClient) VeniceBalance(ctx context.Context, connection Connection) (*VeniceBalanceResult, error) {
	if c == nil {
		return nil, errors.New("Venice balance client is not configured")
	}
	if c.AddressResolver == nil {
		return nil, errors.New("Venice balance wallet resolver is not configured")
	}
	if c.Signer == nil {
		return nil, errors.New("Venice balance signer is not configured")
	}
	walletName := firstNonEmpty(strings.TrimSpace(connection.Auth.Wallet), DefaultVeniceWallet)
	network := firstNonEmpty(strings.TrimSpace(connection.Auth.Network), "base")
	address, err := c.AddressResolver.AddressForChain(ctx, walletName, network)
	if err != nil {
		return nil, fmt.Errorf("resolve Venice wallet address: %w", err)
	}
	endpoint, domain, err := veniceBalanceEndpoint(connection.BaseURL, address)
	if err != nil {
		return nil, err
	}
	signer := c.Signer(walletName)
	if signer == nil {
		return nil, errors.New("Venice balance signer is not configured")
	}
	header, err := (&siwx.Builder{
		Address: address,
		Domain:  domain,
		Signer:  signer,
		Now:     c.now,
	}).Header(ctx, endpoint)
	if err != nil {
		return nil, err
	}

	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = providerHTTPClient(nil)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build Venice balance request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set(siwx.HeaderName, header)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Venice balance request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("Venice balance returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			WalletAddress     string      `json:"walletAddress"`
			BalanceUSD        json.Number `json:"balanceUsd"`
			CanConsume        bool        `json:"canConsume"`
			MinimumTopUpUSD   json.Number `json:"minimumTopUpUsd"`
			SuggestedTopUpUSD json.Number `json:"suggestedTopUpUsd"`
		} `json:"data"`
	}
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode Venice balance response: %w", err)
	}
	if !payload.Success {
		return nil, errors.New("Venice balance response was not successful")
	}
	checkedAt := c.now().UTC().Format(time.RFC3339Nano)
	return &VeniceBalanceResult{
		ConnectionID:      connection.ID,
		Wallet:            walletName,
		Network:           network,
		WalletAddress:     firstNonEmpty(strings.TrimSpace(payload.Data.WalletAddress), address),
		BalanceUSD:        jsonNumberString(payload.Data.BalanceUSD),
		CanConsume:        payload.Data.CanConsume,
		MinimumTopUpUSD:   jsonNumberString(payload.Data.MinimumTopUpUSD),
		SuggestedTopUpUSD: jsonNumberString(payload.Data.SuggestedTopUpUSD),
		CheckedAt:         checkedAt,
	}, nil
}

func (c *VeniceBalanceClient) now() time.Time {
	if c != nil && c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func veniceBalanceEndpoint(baseURL, walletAddress string) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", "", fmt.Errorf("parse Venice base URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", "", fmt.Errorf("Venice base URL must include scheme and host")
	}
	path := strings.TrimRight(parsed.Path, "/")
	if path == "" || path == "/" {
		path = "/api/v1"
	}
	if strings.HasSuffix(path, "/chat/completions") {
		path = strings.TrimSuffix(path, "/chat/completions")
	}
	if !strings.HasSuffix(path, "/x402/balance") {
		path += "/x402/balance"
	}
	parsed.Path = strings.TrimRight(path, "/") + "/" + url.PathEscape(walletAddress)
	parsed.RawQuery = ""
	parsed.Fragment = ""
	domain := parsed.Hostname()
	if domain == "" {
		domain = parsed.Host
	}
	return parsed.String(), domain, nil
}

func jsonNumberString(value json.Number) string {
	if strings.TrimSpace(value.String()) == "" {
		return ""
	}
	return value.String()
}
