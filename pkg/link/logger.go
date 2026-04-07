package link

import (
	"log/slog"

	"github.com/sky10/sky10/pkg/logging"
)

const logComponent = "link"

func defaultLogger(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return logging.WithComponent(nil, logComponent)
}

func componentLogger(logger *slog.Logger) *slog.Logger {
	return logging.WithComponent(defaultLogger(logger), logComponent)
}
