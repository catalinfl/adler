package adler

import "time"

// Config contains runtime tuning knobs for Adler sessions.
//
// Use constructor options (With...) to override individual fields while
// keeping stable defaults for all other values.
type Config struct {
	// WriteWait is the maximum duration allowed for a single outbound write.
	// A zero value disables the per-write deadline.
	// Typical values are between 5 and 30 seconds for internet clients.
	WriteWait time.Duration
	// PongWait is the maximum duration to wait for peer activity before the
	// connection is considered stale by higher-level logic.
	// A zero value disables the idle timeout.
	// If enabled, common values are 30-120 seconds.
	PongWait time.Duration
	// PingPeriod defines how often server-side ping frames are sent.
	// Keep it lower than PongWait when PongWait is enabled.
	// A common ratio is PingPeriod = ~80-90% of PongWait.
	PingPeriod time.Duration
	// MessageBufferSize is the per-session outbound queue capacity.
	// Start with 64-256 for chat-like traffic and increase only if you observe
	// ErrBufferFull under normal expected bursts.
	// Higher values increase per-session memory usage.
	// When this value is 0, runtime falls back to a minimal queue.
	MessageBufferSize int
	// DispatchAsync controls inbound message dispatch mode.
	// true dispatches each message on its own goroutine.
	DispatchAsync bool
	// DeleteRoomOnEmpty controls whether rooms are removed automatically
	// when the last session leaves.
	DeleteRoomOnEmpty bool
}

// Option mutates Config during construction.
type Option func(*Config)

// WithWriteWait overrides Config.WriteWait.
// d is interpreted as seconds: WithWriteWait(10) means 10s.
// Do not pass time.Second-multiplied values here.
func WithWriteWait(d time.Duration) Option {
	return func(c *Config) {
		c.WriteWait = d * time.Second
	}
}

// WithPongWait overrides Config.PongWait.
// d is interpreted as seconds: WithPongWait(60) means 60s.
// Set 0 to disable idle disconnects.
func WithPongWait(d time.Duration) Option {
	return func(c *Config) {
		c.PongWait = d * time.Second
	}
}

// WithPingPeriod overrides Config.PingPeriod.
// d is interpreted as seconds: WithPingPeriod(60) means 60s.
func WithPingPeriod(d time.Duration) Option {
	return func(c *Config) {
		c.PingPeriod = d * time.Second
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

// WithDeleteRoomOnEmpty controls automatic room removal when room size reaches 0.
func WithDeleteRoomOnEmpty(enabled bool) Option {
	return func(c *Config) {
		c.DeleteRoomOnEmpty = enabled
	}
}

// defaultMessageBufferSize balances burst tolerance with per-session memory use.
const defaultMessageBufferSize = 128

// newConfig builds a Config with defaults, then applies options in order.
func newConfig(opts ...Option) *Config {
	cfg := &Config{
		WriteWait:         10 * time.Second,
		PongWait:          0,
		PingPeriod:        60 * time.Second,
		MessageBufferSize: defaultMessageBufferSize,
		DispatchAsync:     true,
		DeleteRoomOnEmpty: true,
	}

	for _, opt := range opts {
		if opt == nil {
			continue
		}

		opt(cfg)
	}

	return cfg
}
