package matchmaking

import (
	"slices"
	"sync"

	"github.com/catalinfl/adler"
)

type Matchmaker struct {
	mu        sync.Mutex
	waitQueue []*adler.Session // Secondary queue, it is not finite
	queue     []*adler.Session
	adler     *adler.Adler
	maxQueue  int
	roomSize  int // Number of players per room
	nextID    int // Name of rooms
}

type MatchmakingConfig struct {
	MaxQueue int
	RoomSize int
}

type MatchmakingOption func(*MatchmakingConfig)

func NewMatchmaker(a *adler.Adler, opts ...MatchmakingOption) *Matchmaker {
	cfg := newMatchmakingConfig(opts...)

	if cfg.RoomSize < 2 {
		cfg.RoomSize = 2
	}

	if cfg.MaxQueue < 0 {
		cfg.MaxQueue = 0 // Unlimited
	}

	return &Matchmaker{
		waitQueue: make([]*adler.Session, 0),
		queue:     make([]*adler.Session, 0, cfg.MaxQueue),
		adler:     a,
		maxQueue:  cfg.MaxQueue,
		roomSize:  cfg.RoomSize,
		nextID:    1,
	}
}

func (m *Matchmaker) AddToQueue(s *adler.Session) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if slices.Contains(m.queue, s) {
		s.WriteJSON(adler.Map{
			"type":    "queue_error",
			"message": "You are already in a queue",
		})
		return
	}

	if slices.Contains(m.waitQueue, s) {
		s.WriteJSON(adler.Map{
			"type":    "wait_queue_error",
			"message": "You are already in a queue",
		})
		return
	}

	if m.maxQueue > 0 && len(m.queue) >= m.maxQueue {
		m.waitQueue = append(m.waitQueue, s)
		s.Set("queue_status", "waiting")

		position := len(m.waitQueue)
		s.WriteJSON(adler.Map{
			"type":     "wait_queue_joined",
			"message":  "Main queue is full. You are now in the waiting queue",
			"position": position,
		})
		return
	}

	// adding to main queue

}

func newMatchmakingConfig(opts ...MatchmakingOption) *MatchmakingConfig {
	cfg := &MatchmakingConfig{
		MaxQueue: 100,
		RoomSize: 4,
	}

	for _, opt := range opts {
		if opt == nil {
			continue
		}

		opt(cfg)
	}

	return cfg
}

func WithRoomSize(roomSize int) MatchmakingOption {
	return func(mc *MatchmakingConfig) {
		mc.RoomSize = roomSize
	}
}

func WithQueueLength(queueLength int) MatchmakingOption {
	return func(mc *MatchmakingConfig) {
		mc.MaxQueue = queueLength
	}
}
