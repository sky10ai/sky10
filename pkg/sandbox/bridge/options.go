package bridge

import (
	"time"

	"github.com/coder/websocket"
)

const (
	defaultMaxFrameSize         = 4 << 20
	defaultKeepaliveInterval    = 25 * time.Second
	defaultKeepalivePingTimeout = 5 * time.Second
)

type options struct {
	maxFrameSize      int64
	keepaliveInterval time.Duration
	keepaliveTimeout  time.Duration
	acceptOptions     *websocket.AcceptOptions
	dialOptions       *websocket.DialOptions
}

// Option configures a bridge connection.
type Option func(*options)

func newOptions(opts ...Option) options {
	cfg := options{
		maxFrameSize:      defaultMaxFrameSize,
		keepaliveInterval: defaultKeepaliveInterval,
		keepaliveTimeout:  defaultKeepalivePingTimeout,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return cfg
}

// WithMaxFrameSize sets the WebSocket read limit for bridge frames.
func WithMaxFrameSize(size int64) Option {
	return func(o *options) {
		o.maxFrameSize = size
	}
}

// WithKeepaliveInterval sets how often bridge connections send WebSocket
// pings while Run is active. A non-positive interval disables keepalives.
func WithKeepaliveInterval(interval time.Duration) Option {
	return func(o *options) {
		o.keepaliveInterval = interval
	}
}

// WithKeepaliveTimeout sets how long a bridge connection waits for a pong
// before closing the socket.
func WithKeepaliveTimeout(timeout time.Duration) Option {
	return func(o *options) {
		o.keepaliveTimeout = timeout
	}
}

// WithAcceptOptions passes AcceptOptions to websocket.Accept.
func WithAcceptOptions(opts *websocket.AcceptOptions) Option {
	return func(o *options) {
		o.acceptOptions = opts
	}
}

// WithDialOptions passes DialOptions to websocket.Dial.
func WithDialOptions(opts *websocket.DialOptions) Option {
	return func(o *options) {
		o.dialOptions = opts
	}
}
