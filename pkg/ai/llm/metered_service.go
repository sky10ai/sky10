package llm

import "context"

const DefaultMeteredServiceAgentID = "sky10-model-api"

// MeteredServiceBackend is the narrow x402-backed call surface the Venice
// adapter needs. The daemon wires this to the existing metered-services
// backend so wallet signing, service approval, receipts, and SIWX remain in
// the x402 layer.
type MeteredServiceBackend interface {
	CallMeteredService(context.Context, MeteredServiceCallParams) (*MeteredServiceCallResult, error)
	StreamMeteredService(context.Context, MeteredServiceCallParams, func([]byte) error) (*MeteredServiceCallResult, error)
}

type MeteredServiceCallParams struct {
	AgentID      string
	ServiceID    string
	Path         string
	Method       string
	Headers      map[string]string
	Body         []byte
	MaxPriceUSDC string
	PaymentNonce string
}

type MeteredServiceCallResult struct {
	Status  int
	Headers map[string]string
	Body    []byte
}
