package matchmaking

import (
	"sync"

	"github.com/catalinfl/adler"
	"github.com/google/uuid"
)

const (
	sessionKeyQueueStatus = "__matchmaking_queue_status"
	sessionKeyRoomID      = "__matchmaking_room_id"

	queueStatusLeft    = "left"
	queueStatusQueued  = "queued"
	queueStatusWaiting = "waiting"
	queueStatusPlaying = "playing"
)

type Matchmaker struct {
	mu             sync.Mutex
	waitQueue      []*adler.Session
	inWaitQueueMap map[*adler.Session]struct{}
	queue          []*adler.Session
	inQueueMap     map[*adler.Session]struct{}
	adler          *adler.Adler
	maxQueue       int
	roomSize       int // Number of players per room
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
		waitQueue:      make([]*adler.Session, 0),
		queue:          make([]*adler.Session, 0, cfg.MaxQueue),
		inWaitQueueMap: map[*adler.Session]struct{}{},
		inQueueMap:     map[*adler.Session]struct{}{},
		adler:          a,
		maxQueue:       cfg.MaxQueue,
		roomSize:       cfg.RoomSize,
	}
}

func (m *Matchmaker) AddToQueue(s *adler.Session) {
	var notifications []func()

	queueStatus, ok := s.GetString(sessionKeyQueueStatus)
	if ok && queueStatus == queueStatusPlaying {
		return
	}

	m.mu.Lock()

	if _, exists := m.inQueueMap[s]; exists {
		m.mu.Unlock()
		s.WriteJSON(adler.Map{
			"type":    "queue_error",
			"message": "You are already in a queue",
		})
		return
	}

	if _, exists := m.inWaitQueueMap[s]; exists {
		m.mu.Unlock()
		s.WriteJSON(adler.Map{
			"type":    "wait_queue_error",
			"message": "You are already in a queue",
		})
		return
	}

	if m.maxQueue > 0 && len(m.queue) >= m.maxQueue {
		m.waitQueue = append(m.waitQueue, s)
		m.inWaitQueueMap[s] = struct{}{}
		s.Set(sessionKeyQueueStatus, queueStatusWaiting)
		notifications = append(notifications, m.processQueueTransitions()...)
	} else {
		notifications = append(notifications, m.addToMainQueue(s))
		notifications = append(notifications, m.processQueueTransitions()...)
	}

	m.mu.Unlock()

	for _, n := range notifications {
		n()
	}
}

func (m *Matchmaker) addToMainQueue(s *adler.Session) func() {
	m.queue = append(m.queue, s)
	m.inQueueMap[s] = struct{}{}

	delete(m.inWaitQueueMap, s)
	s.Set(sessionKeyQueueStatus, queueStatusQueued)

	return func() {
		s.WriteJSON(adler.Map{
			"type":    "queue_joined",
			"message": "You have joined the main queue",
		})
	}
}

func (m *Matchmaker) processQueueTransitions() []func() {
	var notifications []func()

	notifications = append(notifications, m.tryCreateRoom()...)
	notifications = append(notifications, m.promoteFromWaitingQueue()...)
	notifications = append(notifications, m.tryCreateRoom()...)

	return notifications
}

func (m *Matchmaker) tryCreateRoom() []func() {
	var notifications []func()

	for len(m.queue) >= m.roomSize {
		players := make([]*adler.Session, m.roomSize)
		copy(players, m.queue[:m.roomSize])
		clear(m.queue[:m.roomSize])
		m.queue = m.queue[m.roomSize:]

		id, _ := uuid.NewV7()
		roomID := "room_" + id.String()
		room := m.adler.NewRoom(roomID)

		for _, player := range players {
			delete(m.inQueueMap, player)
			room.Join(player)
			player.Set(sessionKeyQueueStatus, queueStatusPlaying)
			player.Set(sessionKeyRoomID, roomID)
		}

		notifications = append(notifications, func() {
			room.BroadcastJSON(adler.Map{
				"type":    "match_found",
				"room_id": roomID,
			})
		})
	}

	return notifications
}

func (m *Matchmaker) promoteFromWaitingQueue() []func() {
	var notifications []func()

	for len(m.waitQueue) > 0 && (m.maxQueue == 0 || len(m.queue) < m.maxQueue) {
		next := m.waitQueue[0]
		m.waitQueue[0] = nil
		m.waitQueue = m.waitQueue[1:]
		delete(m.inWaitQueueMap, next)
		m.queue = append(m.queue, next)
		m.inQueueMap[next] = struct{}{}
		next.Set(sessionKeyQueueStatus, queueStatusQueued)

		notifications = append(notifications, func() {
			next.WriteJSON(adler.Map{
				"type":    "promoted_to_queue",
				"message": "A spot opened up. You are now in the main queue",
			})
		})
	}

	return notifications
}

func (m *Matchmaker) RemoveFromQueue(s *adler.Session) {
	var notifications []func()

	m.mu.Lock()

	if m.removeFromSlice(&m.queue, m.inQueueMap, s) {
		s.Set(sessionKeyQueueStatus, queueStatusLeft)
		notifications = append(notifications, m.processQueueTransitions()...)
	} else if m.removeFromSlice(&m.waitQueue, m.inWaitQueueMap, s) {
		s.Set(sessionKeyQueueStatus, queueStatusLeft)
	}

	m.mu.Unlock()

	for _, n := range notifications {
		n()
	}
}

func (m *Matchmaker) removeFromSlice(slice *[]*adler.Session, mapSession map[*adler.Session]struct{}, s *adler.Session) bool {
	items := *slice
	for i, session := range items {
		if session == s {
			copy(items[i:], items[i+1:])
			items[len(items)-1] = nil
			*slice = items[:len(items)-1]
			delete(mapSession, s)
			return true
		}
	}
	return false
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

	if cfg.MaxQueue > 0 && cfg.RoomSize > cfg.MaxQueue {
		panic("room size is bigger than max queue")
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
