package update

import (
	"log/slog"

	"github.com/sky10/sky10/pkg/logging"
)

const logComponent = "update"

func logger() *slog.Logger {
	return logging.WithComponent(nil, logComponent)
}
