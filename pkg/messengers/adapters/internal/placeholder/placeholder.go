package placeholder

import (
	"context"
	"fmt"
	"io"

	"github.com/sky10/sky10/pkg/messengers/adapters/shared"
)

// Definition builds one simple adapter definition backed by a placeholder
// server stub until the real adapter implementation lands.
func Definition(name, summary string, serve shared.ServeFunc) shared.Definition {
	return shared.Definition{
		Name:    name,
		Summary: summary,
		Serve:   serve,
	}
}

// Serve is the placeholder adapter entry point used before a real adapter
// server is implemented.
func Serve(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string) error {
	_, _, _, _ = ctx, stdin, stdout, stderr
	return fmt.Errorf("messaging adapter %q is not implemented yet", name)
}
