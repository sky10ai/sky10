package rpc

import "context"

type callerContextKey struct{}

// CallerInfo describes the origin of an RPC request.
type CallerInfo struct {
	Transport string
	Remote    string
}

// WithCallerInfo annotates a context with RPC caller metadata.
func WithCallerInfo(ctx context.Context, transport, remote string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, callerContextKey{}, CallerInfo{
		Transport: transport,
		Remote:    remote,
	})
}

// CallerInfoFromContext extracts RPC caller metadata from ctx.
func CallerInfoFromContext(ctx context.Context) (CallerInfo, bool) {
	if ctx == nil {
		return CallerInfo{}, false
	}
	info, ok := ctx.Value(callerContextKey{}).(CallerInfo)
	return info, ok
}
