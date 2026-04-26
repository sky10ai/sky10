package shared

import (
	"context"
	"io"

	"github.com/sky10/sky10/pkg/messaging"
	messagingexternal "github.com/sky10/sky10/pkg/messaging/external"
)

// ServeFunc runs one adapter JSON-RPC server over the provided stdio streams.
type ServeFunc func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error

// Definition describes one built-in messaging adapter exposed through
// `sky10 messaging <adapter>`.
type Definition struct {
	Name    string
	Summary string
	Serve   ServeFunc

	// Adapter, Settings, and Actions describe the generic UX schema that the
	// settings UI uses to render this adapter, mirroring what external
	// adapters declare in their adapter.json. They are optional: built-ins
	// without a UX schema can leave them zero, but the settings page will
	// then have nothing to render for them.
	Adapter  messaging.Adapter
	Settings []messagingexternal.Setting
	Actions  []messagingexternal.Action
}
