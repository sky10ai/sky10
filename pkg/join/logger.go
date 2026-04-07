package join

import (
	"log/slog"

	"github.com/sky10/sky10/pkg/logging"
)

const logComponent = "join"

func componentLogger(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		logger = slog.Default()
	}
	return logging.WithComponent(logger, logComponent)
}
