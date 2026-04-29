package matchmaking

import (
	"container/list"
	"sync"
	"time"

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

type matchmakingCommandKind int8

const (
	matchmakingCommandAdd matchmakingCommandKind = iota
	matchmakingCommandRemove
)

type queueItem struct {
	session    *adler.Session
	enqueuedAt time.Time
}

type matchmakingCommand struct {
	kind    matchmakingCommandKind
	session *adler.Session
	reply   chan []func()
}

type Matchmaker struct {
	commands           chan matchmakingCommand
	mu                 sync.Mutex
	waitQueue          *list.List
	queue              *list.List
	inWaitQueueMap     map[*adler.Session]*list.Element
	inQueueMap         map[*adler.Session]*list.Element
	adler              *adler.Adler
	maxQueue           int
	roomSize           int // Number of players per room
	minRoomSize        int // Minimum number of players until the game should start
	partialRoomTimeout time.Duration
}

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

func (m *Matchmaker) run() {
	var tick <-chan time.Time
	var ticker *time.Ticker

	if m.partialRoomTimeout > 0 {
		ticker = time.NewTicker(matchmakingTickInterval(m.partialRoomTimeout))
		tick = ticker.C
		defer ticker.Stop()
	}

	for {
		select {
		case cmd := <-m.commands:
			now := time.Now()
			var notifications []func()

			switch cmd.kind {
			case matchmakingCommandAdd:
				notifications = m.handleAddToQueue(cmd.session, now)
			case matchmakingCommandRemove:
				notifications = m.handleRemoveFromQueue(cmd.session, now)
			}

			cmd.reply <- notifications

		case now := <-tick:
			notifications := m.processQueueTransitions(now)
			if len(notifications) > 0 {
				go runNotifications(notifications)
			}
		}
	}

}

func (m *Matchmaker) AddToQueue(s *adler.Session) {
	if s == nil {
		return
	}

	reply := make(chan []func(), 1)

	m.commands <- matchmakingCommand{
		kind:    matchmakingCommandAdd,
		session: s,
		reply:   reply,
	}

	runNotifications(<-reply)
}

func (m *Matchmaker) RemoveFromQueue(s *adler.Session) {
	if s == nil {
		return
	}

	reply := make(chan []func(), 1)

	m.commands <- matchmakingCommand{
		kind:    matchmakingCommandRemove,
		session: s,
		reply:   reply,
	}

	runNotifications(<-reply)
}

func (m *Matchmaker) handleAddToQueue(s *adler.Session, now time.Time) []func() {
	var notifications []func()

	queueStatus, ok := s.GetString(sessionKeyQueueStatus)
	if ok && queueStatus == queueStatusPlaying {
		return nil
	}

	if _, exists := m.inQueueMap[s]; exists {
		return []func(){
			func() {
				s.WriteJSON(adler.Map{
					"type":    "queue_error",
					"message": "You are already in a queue",
				})
			},
		}
	}

	if _, exists := m.inWaitQueueMap[s]; exists {
		return []func(){
			func() {
				s.WriteJSON(adler.Map{
					"type":    "wait_queue_error",
					"message": "You are already in a queue",
				})
			},
		}
	}

	if m.maxQueue > 0 && m.queue.Len() >= m.maxQueue {
		notifications = append(notifications, m.addToWaitingQueue(s, now))
		notifications = append(notifications, m.processQueueTransitions(now)...)
		return notifications
	}

	notifications = append(notifications, m.addToMainQueue(s, now))
	notifications = append(notifications, m.processQueueTransitions(now)...)

	return notifications
}

func (m *Matchmaker) addToMainQueue(s *adler.Session, now time.Time) func() {
	item := &queueItem{
		session:    s,
		enqueuedAt: now,
	}

	element := m.queue.PushBack(item)
	m.inQueueMap[s] = element
	delete(m.inWaitQueueMap, s)
	s.Set(sessionKeyQueueStatus, queueStatusQueued)

	return func() {
		s.WriteJSON(adler.Map{
			"type":    "queue_joined",
			"message": "You have joined the main queue",
		})
	}
}

func (m *Matchmaker) addToWaitingQueue(s *adler.Session, now time.Time) func() {
	item := &queueItem{
		session:    s,
		enqueuedAt: now,
	}

	element := m.waitQueue.PushBack(item)
	m.inWaitQueueMap[s] = element
	s.Set(sessionKeyQueueStatus, queueStatusWaiting)

	return func() {
		s.WriteJSON(adler.Map{
			"type":    "wait_queue_joined",
			"message": "The main queue is full. You have joined the waiting queue.",
		})
	}
}

func (m *Matchmaker) processQueueTransitions(now time.Time) []func() {
	var notifications []func()

	notifications = append(notifications, m.createFullRooms()...)
	notifications = append(notifications, m.promoteFromWaitingQueue(now)...)
	notifications = append(notifications, m.createFullRooms()...)

	partialRoomNotifications := m.createPartialRoomIfAllowed(now)
	if len(partialRoomNotifications) > 0 {
		notifications = append(notifications, partialRoomNotifications...)
		notifications = append(notifications, m.promoteFromWaitingQueue(now)...)
		notifications = append(notifications, m.createFullRooms()...)
	}

	return notifications
}

func (m *Matchmaker) createFullRooms() []func() {
	var notifications []func()

	for m.queue.Len() >= m.roomSize {
		notifications = append(notifications, m.createRoomFromQueue(m.roomSize))
	}

	return notifications
}

func (m *Matchmaker) createPartialRoomIfAllowed(now time.Time) []func() {
	if m.queue.Len() < m.minRoomSize {
		return nil
	}

	if m.partialRoomTimeout > 0 {
		front := m.queue.Front()
		if front == nil {
			return nil
		}

		item := front.Value.(*queueItem)
		if now.Sub(item.enqueuedAt) < m.partialRoomTimeout { // must stay at least partialRoomTimeout
			return nil
		}
	}

	size := m.queue.Len()
	if size > m.roomSize {
		size = m.roomSize
	}

	return []func(){
		m.createRoomFromQueue(size),
	}
}

func (m *Matchmaker) createRoomFromQueue(size int) func() {
	players := make([]*adler.Session, 0, size)
	for i := 0; i < size; i++ {
		front := m.queue.Front()
		if front == nil {
			break
		}

		item := front.Value.(*queueItem)
		m.queue.Remove(front)
		delete(m.inQueueMap, item.session)

		players = append(players, item.session)
	}

	id, err := uuid.NewV7()
	if err != nil {
		id = uuid.New()
	}

	roomID := "room_" + id.String()
	room := m.adler.NewRoom(roomID)

	for _, player := range players {
		room.Join(player)
		player.Set(sessionKeyQueueStatus, queueStatusPlaying)
		player.Set(sessionKeyRoomID, roomID)
	}

	return func() {
		room.BroadcastJSON(adler.Map{
			"type":    "match_found",
			"room_id": roomID,
			"players": len(players),
		})
	}
}

func (m *Matchmaker) promoteFromWaitingQueue(now time.Time) []func() {
	var notifications []func()

	for m.waitQueue.Len() > 0 && (m.maxQueue == 0 || m.queue.Len() < m.maxQueue) {
		front := m.waitQueue.Front()
		if front == nil {
			break
		}

		item := front.Value.(*queueItem)
		session := item.session

		m.waitQueue.Remove(front)
		delete(m.inWaitQueueMap, session)

		newItem := &queueItem{
			session:    session,
			enqueuedAt: now, // changing the current enqueue time from waiting queue to normal queue
		}

		element := m.queue.PushBack(newItem)
		m.inQueueMap[session] = element

		session.Set(sessionKeyQueueStatus, queueStatusQueued)

		notifications = append(notifications, func() {
			session.WriteJSON(adler.Map{
				"type":    "promoted_to_queue",
				"message": "A spot opened up. You are now in the main queue",
			})
		})
	}

	return notifications
}

func runNotifications(notifications []func()) {
	for _, notification := range notifications {
		if notification != nil {
			notification()
		}
	}
}

func (m *Matchmaker) handleRemoveFromQueue(s *adler.Session, now time.Time) []func() {
	var notifications []func()

	if element, exists := m.inQueueMap[s]; exists {
		m.queue.Remove(element)
		delete(m.inQueueMap, s)

		s.Set(sessionKeyQueueStatus, queueStatusLeft)
		notifications = append(notifications, m.processQueueTransitions(now)...)
		return notifications
	}

	if element, exists := m.inWaitQueueMap[s]; exists {
		m.waitQueue.Remove(element)
		delete(m.inWaitQueueMap, s)
		s.Set(sessionKeyQueueStatus, queueStatusLeft)
		return nil
	}

	return nil
}

func clampTime(v, min, max time.Duration) time.Duration {
	if v <= min {
		return min
	}

	if v >= max {
		return max
	}

	return v
}

func matchmakingTickInterval(timeout time.Duration) time.Duration {
	return clampTime(timeout, 10*time.Millisecond, 1*time.Second)
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

	cfg.MinRoomSize = min(cfg.RoomSize, cfg.MinRoomSize)

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
