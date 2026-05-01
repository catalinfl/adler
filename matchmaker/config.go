// Package matchmaking provides a queue-driven room allocator on top of Adler.
//
// A Matchmaker keeps sessions in an internal main queue and, optionally, a
// waiting queue when the main queue is full. A background goroutine owns those
// queues, processes add/remove requests through a command channel, and creates
// a new Adler room as soon as enough sessions are available.
//
// The room assignment is stored in each session so your application can inspect
// or clear the current match state later, for example from OnRoomLeave or
// HandleDisconnect.
package matchmaking

import (
	"container/list"
	"time"

	"github.com/catalinfl/adler"
)

// MatchmakingConfig controls how the matchmaker buffers sessions and when it
// turns queued sessions into rooms.
//
// The defaults are chosen to make the queue usable out of the box: a bounded
// main queue, a room size of 4, and a command buffer large enough for bursty
// connect/disconnect traffic.
type MatchmakingConfig struct {
	MaxQueue           int
	RoomSize           int
	MinRoomSize        int
	PartialRoomTimeout time.Duration
	CommandBuffer      int
}

// MatchmakingOption customizes the configuration used by NewMatchmaker.
//
// Options are applied before the matchmaker starts, so they only affect the
// queues and thresholds created during initialization.
type MatchmakingOption func(*MatchmakingConfig)

// NewMatchmaker creates a queue manager that runs in the background and groups
// sessions into rooms automatically.
//
// Internally it starts a goroutine that receives add/remove commands over a
// channel. That goroutine owns the queue state, so calls such as AddToQueue and
// RemoveFromQueue do not mutate the lists directly; they only enqueue commands
// for the worker to process. When the main queue reaches RoomSize, or when a
// partial room is allowed by the timeout/min-size rules, the matchmaker creates
// an Adler room, joins the selected sessions to it, stores the room id in each
// session, and broadcasts a match_found event.
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

// WithRoomSize sets how many sessions are placed into each generated room.
//
// If RoomSize is 2, the matchmaker will wait until two queued sessions are
// available before creating a room, unless partial room creation is permitted
// by the timeout/min-size rules.
func WithRoomSize(roomSize int) MatchmakingOption {
	return func(mc *MatchmakingConfig) {
		mc.RoomSize = roomSize
	}
}

// WithMinRoomSize sets the minimum number of queued sessions needed before the
// matchmaker may create a partial room.
//
// This only matters when there are not enough sessions to fill a full room.
// The matchmaker still respects RoomSize as the upper bound for that partial
// room.
func WithMinRoomSize(minRoomSize int) MatchmakingOption {
	return func(mc *MatchmakingConfig) {
		mc.MinRoomSize = minRoomSize
	}
}

// WithQueueLength limits how many sessions can stay in the main queue.
//
// When the main queue is full, new sessions are moved to the waiting queue and
// are promoted automatically later when room creation frees a slot.
func WithQueueLength(queueLength int) MatchmakingOption {
	return func(mc *MatchmakingConfig) {
		mc.MaxQueue = queueLength
	}
}

// WithPartialRoomTimeout controls how long the oldest queued session must wait
// before the matchmaker is allowed to create a room with fewer than RoomSize
// players.
//
// A zero timeout disables partial-room creation entirely, which means the
// matchmaker waits for a full room.
func WithPartialRoomTimeout(timeout time.Duration) MatchmakingOption {
	return func(mc *MatchmakingConfig) {
		mc.PartialRoomTimeout = timeout
	}
}

// WithCommandBuffer sets the buffer size for the internal command channel.
//
// A larger buffer allows more concurrent AddToQueue and RemoveFromQueue calls
// to be accepted before the worker catches up.
func WithCommandBuffer(size int) MatchmakingOption {
	return func(mc *MatchmakingConfig) {
		mc.CommandBuffer = size
	}
}
