package imapsmtp

import (
	"context"
	"io"

	"github.com/sky10/sky10/pkg/messengers/adapters/internal/placeholder"
)

var Definition = placeholder.Definition("imap-smtp", "Built-in IMAP/SMTP messaging adapter", serve)

func serve(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
	return placeholder.Serve(ctx, stdin, stdout, stderr, "imap-smtp")
}
