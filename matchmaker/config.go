package matchmaking

import (
	"container/list"
	"time"

	"github.com/catalinfl/adler"
)

type MatchmakingConfig struct {
	MaxQueue           int
	RoomSize           int
	MinRoomSize        int
	PartialRoomTimeout time.Duration
	CommandBuffer      int
}

type MatchmakingOption func(*MatchmakingConfig)

func NewMatchmaker(a *adler.Adler, opts ...MatchmakingOption) *Matchmaker {
	cfg := newMatchmakingConfig(opts...)

	m := &Matchmaker{
		commands:           make(chan matchmakingCommand, cfg.CommandBuffer),
		waitQueue:          list.New(),
		queue:              list.New(),
		inWaitQueueMap:     map[*adler.Session]*list.Element{},
		inQueueMap:         map[*adler.Session]*list.Element{},
		adler:              a,
		maxQueue:           cfg.MaxQueue,
		roomSize:           cfg.RoomSize,
		minRoomSize:        cfg.MinRoomSize,
		partialRoomTimeout: cfg.PartialRoomTimeout,
	}

	go m.run()
	return m
}

func newMatchmakingConfig(opts ...MatchmakingOption) *MatchmakingConfig {
	cfg := &MatchmakingConfig{
		MaxQueue:           100,
		RoomSize:           4,
		MinRoomSize:        0,
		PartialRoomTimeout: 0,
		CommandBuffer:      1024,
	}

	for _, opt := range opts {
		if opt == nil {
			continue
		}

		opt(cfg)
	}

	if cfg.RoomSize < 2 {
		cfg.RoomSize = 2
	}

	if cfg.MinRoomSize <= 0 {
		cfg.MinRoomSize = cfg.RoomSize
	}

	cfg.MinRoomSize = min(cfg.RoomSize, cfg.MinRoomSize) // if MinRoomSize is bigger than RoomSize, set MinRoomSize as RoomSize

	if cfg.MaxQueue < 0 {
		cfg.MaxQueue = 0
	}

	if cfg.CommandBuffer <= 0 {
		cfg.CommandBuffer = 1024
	}

	return cfg
}

func WithRoomSize(roomSize int) MatchmakingOption {
	return func(mc *MatchmakingConfig) {
		mc.RoomSize = roomSize
	}
}

func WithMinRoomSize(minRoomSize int) MatchmakingOption {
	return func(mc *MatchmakingConfig) {
		mc.MinRoomSize = minRoomSize
	}
}

func WithQueueLength(queueLength int) MatchmakingOption {
	return func(mc *MatchmakingConfig) {
		mc.MaxQueue = queueLength
	}
}

func WithPartialRoomTimeout(timeout time.Duration) MatchmakingOption {
	return func(mc *MatchmakingConfig) {
		mc.PartialRoomTimeout = timeout
	}
}

func WithCommandBuffer(size int) MatchmakingOption {
	return func(mc *MatchmakingConfig) {
		mc.CommandBuffer = size
	}
}
