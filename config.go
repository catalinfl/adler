package adler

import "time"

type Config struct {
	WriteWait         time.Duration
	PongWait          time.Duration
	PingPeriod        time.Duration
	MaxMessageSize    int64
	MessageBufferSize int32
}

func newConfig() *Config {
	return &Config{
		WriteWait:         10 * time.Second,
		PongWait:          60 * time.Second,
		PingPeriod:        54 * time.Second,
		MaxMessageSize:    2 << 10, // 1 kB
		MessageBufferSize: 2 << 8,  // 512B
	}
}
