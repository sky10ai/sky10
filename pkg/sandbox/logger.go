package sandbox

import (
	"log/slog"

	"github.com/sky10/sky10/pkg/logging"
)

const logComponent = "sandbox"

func componentLogger(logger *slog.Logger) *slog.Logger {
	return logging.WithComponent(logger, logComponent)
}
