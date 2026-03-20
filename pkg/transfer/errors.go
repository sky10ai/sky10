package transfer

import "errors"

// ErrIdleTimeout is returned when no bytes are transferred for the
// configured idle timeout duration.
var ErrIdleTimeout = errors.New("transfer: idle timeout")
