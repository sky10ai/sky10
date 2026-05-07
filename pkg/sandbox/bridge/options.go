package bridge

import "github.com/coder/websocket"

const defaultMaxFrameSize = 4 << 20

type options struct {
	maxFrameSize  int64
	acceptOptions *websocket.AcceptOptions
	dialOptions   *websocket.DialOptions
}

// Option configures a bridge connection.
type Option func(*options)

func newOptions(opts ...Option) options {
	cfg := options{maxFrameSize: defaultMaxFrameSize}
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
