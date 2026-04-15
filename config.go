package adler

import "time"

// Config contains runtime tuning knobs for Adler sessions.
//
// Use constructor options (With...) to override individual fields while
// keeping stable defaults for all other values.
type Config struct {
	// WriteWait is the maximum duration allowed for a single outbound write.
	// A zero value disables the per-write deadline.
	WriteWait time.Duration
	// PongWait is the maximum duration to wait for peer activity before the
	// connection is considered stale by higher-level logic.
	PongWait time.Duration
	// PingPeriod defines how often server-side ping frames are sent.
	// This should normally stay below PongWait.
	PingPeriod time.Duration
	// MessageBufferSize is the per-session outbound queue capacity.
	// When this value is 0, runtime falls back to a minimal queue.
	MessageBufferSize int
	// DispatchAsync controls inbound message dispatch mode.
	// true dispatches each message on its own goroutine.
	DispatchAsync bool
}

// Option mutates Config during construction.
type Option func(*Config)

// WithWriteWait overrides Config.WriteWait.
func WithWriteWait(d time.Duration) Option {
	return func(c *Config) {
		c.WriteWait = d
	}
}

// WithPongWait overrides Config.PongWait.
func WithPongWait(d time.Duration) Option {
	return func(c *Config) {
		c.PongWait = d
	}
}

// WithPingPeriod overrides Config.PingPeriod.
func WithPingPeriod(d time.Duration) Option {
	return func(c *Config) {
		c.PingPeriod = d
	}
}

// WithMessageBufferSize overrides Config.MessageBufferSize.
func WithMessageBufferSize(size int) Option {
	return func(c *Config) {
		c.MessageBufferSize = size
	}
}

// WithDispatchAsync overrides Config.DispatchAsync.
func WithDispatchAsync(dispatch bool) Option {
	return func(c *Config) {
		c.DispatchAsync = dispatch
	}
}

// defaultMessageBufferSize balances burst tolerance with per-session memory use.
const defaultMessageBufferSize = 128

// newConfig builds a Config with defaults, then applies options in order.
func newConfig(opts ...Option) *Config {
	cfg := &Config{
		WriteWait:         10 * time.Second,
		PongWait:          60 * time.Second,
		PingPeriod:        54 * time.Second,
		MessageBufferSize: defaultMessageBufferSize,
		DispatchAsync:     true,
	}

	for _, opt := range opts {
		if opt == nil {
			continue
		}

		opt(cfg)
	}

	return cfg
}
