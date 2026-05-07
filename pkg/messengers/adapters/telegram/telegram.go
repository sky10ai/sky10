package telegram

import (
	"context"
	"io"

	"github.com/sky10/sky10/pkg/messengers/adapters/shared"
)

// Definition exposes the built-in Telegram bot adapter through
// `sky10 messaging telegram`.
var Definition = shared.Definition{
	Name:     "telegram",
	Summary:  summary,
	Serve:    serve,
	Adapter:  adapterMeta,
	Settings: adapterSettings,
	Actions:  adapterActions,
}

func serve(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
	return newServer().Serve(ctx, stdin, stdout, stderr)
}
