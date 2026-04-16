package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/sky10/sky10/pkg/logging"
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

// Backend is a provider-specific LLM implementation used behind the generic
// OpenAI-compatible proxy surface.
type Backend interface {
	Ready() error
	Forward(ctx context.Context, path, rawQuery, method string, headers http.Header, body []byte) (*http.Response, error)
	TopUp(ctx context.Context, amountUSD string) (string, error)
}

// Config controls the generic LLM proxy path.
type Config struct {
	PathPrefix string
}

// Proxy exposes a provider-neutral OpenAI-compatible HTTP surface.
type Proxy struct {
	pathPrefix string
	backend    Backend
	logger     *slog.Logger
}

type manualTopUpRequest struct {
	AmountUSD string `json:"amountUsd"`
}

// NewProxy builds a provider-neutral LLM proxy.
func NewProxy(cfg Config, backend Backend, logger *slog.Logger) (*Proxy, error) {
	if cfg.PathPrefix == "" {
		cfg.PathPrefix = "/llm/v1"
	}
	if !strings.HasPrefix(cfg.PathPrefix, "/") {
		return nil, fmt.Errorf("llm path prefix must start with '/'")
	}
	if backend == nil {
		return nil, fmt.Errorf("llm backend is required")
	}
	return &Proxy{
		pathPrefix: strings.TrimRight(cfg.PathPrefix, "/"),
		backend:    backend,
		logger:     logging.WithComponent(logger, "llm.proxy"),
	}, nil
}

// HandleAPI proxies provider requests under the configured LLM path prefix.
func (p *Proxy) HandleAPI(w http.ResponseWriter, r *http.Request) {
	if err := p.backend.Ready(); err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	relativePath := strings.TrimPrefix(r.URL.Path, p.pathPrefix)
	if relativePath == r.URL.Path || relativePath == "" {
		writeJSONError(w, http.StatusNotFound, "unknown llm route")
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

	resp, err := p.backend.Forward(r.Context(), relativePath, r.URL.RawQuery, r.Method, cloneHeader(r.Header), body)
	if err != nil {
		p.logger.Warn("llm proxy request failed", "path", relativePath, "method", r.Method, "error", err)
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()

	copyResponse(w, resp)
}

func (p *Proxy) handleManualTopUp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, fmt.Sprintf("use POST for %s/x402/top-up", p.pathPrefix))
		return
	}

	var req manualTopUpRequest
	if r.Body != nil {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "failed to read request body")
			return
		}
		if len(bytes.TrimSpace(body)) > 0 {
			if err := json.Unmarshal(body, &req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
				return
			}
		}
	}

	amount, err := p.backend.TopUp(r.Context(), req.AmountUSD)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":    "ok",
		"amountUsd": amount,
	})
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
