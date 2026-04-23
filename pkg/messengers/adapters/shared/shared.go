package shared

import (
	"context"
	"io"
)

// ServeFunc runs one adapter JSON-RPC server over the provided stdio streams.
type ServeFunc func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error

// Definition describes one built-in messaging adapter exposed through
// `sky10 messaging <adapter>`.
type Definition struct {
	Name    string
	Summary string
	Serve   ServeFunc
}
