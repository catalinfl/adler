package matchmaking

import (
	"fmt"
	"sync"
	"time"

	"github.com/catalinfl/adler"
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
	nextID         int // Name of rooms
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
		nextID:         1,
	}
}

func (m *Matchmaker) AddToQueue(s *adler.Session) {
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
		s.Set("queue_status", "waiting")
	} else {
		m.addToMainQueue(s)
		m.processQueueTransitions()
	}

	m.mu.Unlock()
}

func (m *Matchmaker) addToMainQueue(s *adler.Session) {
	m.queue = append(m.queue, s)
	m.inQueueMap[s] = struct{}{}
	delete(m.inWaitQueueMap, s)
	s.Set("queue_status", "queued")

	position := len(m.queue)
	totalNeeded := m.roomSize

	s.WriteJSON(adler.Map{
		"type":     "queue_joined",
		"message":  "You have joined the main queue",
		"position": position,
		"needed":   totalNeeded,
	})
}

func (m *Matchmaker) processQueueTransitions() {
	m.tryCreateRoom()
	m.promoteFromWaitingQueue()
	m.tryCreateRoom()
}

func (m *Matchmaker) tryCreateRoom() {
	for len(m.queue) >= m.roomSize {
		players := make([]*adler.Session, m.roomSize)
		copy(players, m.queue[:m.roomSize])
		m.queue = m.queue[m.roomSize:]

		roomID := fmt.Sprintf("room_%d", m.nextID)
		m.nextID++
		room := m.adler.NewRoom(roomID)

		playerIDs := make([]string, 0, len(players))

		for _, player := range players {
			delete(m.inQueueMap, player)
			room.Join(player)
			player.Set("queue_status", "playing")
			player.Set("room_id", roomID)
			playerIDs = append(playerIDs, player.RemoteAddr().String())
		}

		m.mu.Unlock()

		room.BroadcastJSON(adler.Map{
			"type":      "match_found",
			"room_id":   roomID,
			"players":   playerIDs,
			"timestamp": time.Now().Unix(),
		})
	}
}

func (m *Matchmaker) promoteFromWaitingQueue() {
	for len(m.waitQueue) > 0 && (m.maxQueue == 0 || len(m.queue) < m.maxQueue) {
		next := m.waitQueue[0]
		m.waitQueue = m.waitQueue[1:]
		delete(m.inWaitQueueMap, next)
		m.queue = append(m.queue, next)
		m.inQueueMap[next] = struct{}{}
		next.Set("queue_status", "queued")

		next.WriteJSON(adler.Map{
			"type":    "promoted_to_queue",
			"message": "A spot opened up. You are now in the main queue",
		})
	}
}

func (m *Matchmaker) RemoveFromQueue(s *adler.Session) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.removeFromSlice(&m.queue, m.inQueueMap, s) {
		delete(m.inQueueMap, s)
		s.Set("queue_status", "left")
		m.processQueueTransitions()
		return
	}

	if m.removeFromSlice(&m.waitQueue, m.inWaitQueueMap, s) {
		delete(m.inWaitQueueMap, s)
		s.Set("queue_status", "left")
		return
	}
}

func (m *Matchmaker) removeFromSlice(slice *[]*adler.Session, mapSession map[*adler.Session]struct{}, s *adler.Session) bool {
	for i, session := range *slice {
		if session == s {
			*slice = append((*slice)[:i], (*slice)[i+1:]...)
			return true
		}
	}
	delete(mapSession, s)
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
