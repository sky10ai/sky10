package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultProviderHTTPTimeout = 2 * time.Minute

type providerRequestOptions struct {
	Provider  string
	Operation string
	URL       string
	Accept    string
	Headers   map[string]string
	Body      interface{}
}

func providerHTTPClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return &http.Client{Timeout: defaultProviderHTTPTimeout}
}

func newProviderJSONRequest(ctx context.Context, opts providerRequestOptions) (*http.Request, error) {
	buf, err := json.Marshal(opts.Body)
	if err != nil {
		return nil, fmt.Errorf("encode %s request: %w", opts.Operation, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, opts.URL, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", firstNonEmpty(opts.Accept, "application/json"))
	for key, value := range opts.Headers {
		if strings.TrimSpace(value) != "" {
			req.Header.Set(key, value)
		}
	}
	return req, nil
}

func providerDoJSON(ctx context.Context, client *http.Client, opts providerRequestOptions, out interface{}) error {
	req, err := newProviderJSONRequest(ctx, opts)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", opts.Operation, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return providerHTTPError(opts.Provider, resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s response: %w", opts.Provider, err)
	}
	return nil
}

func providerDoStream(ctx context.Context, client *http.Client, opts providerRequestOptions) (io.ReadCloser, error) {
	req, err := newProviderJSONRequest(ctx, opts)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", opts.Operation, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, providerHTTPError(opts.Provider, resp)
	}
	return resp.Body, nil
}

func providerHTTPError(provider string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = resp.Status
	}
	return fmt.Errorf("%s upstream returned HTTP %d: %s", provider, resp.StatusCode, msg)
}
