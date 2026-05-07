package matchmaking

import (
	"container/list"
	"errors"
	"time"

	"github.com/catalinfl/adler"
	"github.com/catalinfl/adler/matchmaker/github.com/catalinfl/adler/matchmaking/pb"
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

var (
	// ErrSessionNil is returned when a queue operation receives a nil session.
	//
	// The matchmaker cannot enqueue or remove a nil session because the worker
	// needs a stable pointer to track the session in its internal maps.
	ErrSessionNil = errors.New("session is nil")
	// ErrMatchmakerBusy is returned when AddToQueue cannot place a command into
	// the internal command buffer immediately.
	//
	// This means the matchmaker goroutine has not yet consumed previous work,
	// so the caller can retry later instead of blocking the connect flow.
	ErrMatchmakerBusy = errors.New("matchmaker busy")
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
}

// Matchmaker owns the matchmaking queues and turns sessions into rooms.
//
// Public methods do not modify the queues directly. They send commands to an
// internal worker goroutine, which serializes all queue mutations. That design
// keeps queue state consistent while still letting the rest of the application
// call AddToQueue and RemoveFromQueue from request handlers, connect hooks, or
// disconnect hooks.
type Matchmaker struct {
	commands           chan matchmakingCommand
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
			switch cmd.kind {
			case matchmakingCommandAdd:
				m.handleAddToQueue(cmd.session, now)
			case matchmakingCommandRemove:
				m.handleRemoveFromQueue(cmd.session, now)
			}

		case now := <-tick:
			m.processQueueTransitions(now)
		}
	}
}

// AddToQueue asks the matchmaker to place a session into the active queue.
//
// The call is non-blocking. It only enqueues a command for the worker goroutine
// and returns ErrMatchmakerBusy if the command buffer is full. When the worker
// eventually processes the request, it either places the session into the main
// queue, moves it to the waiting queue, or leaves it alone if the session is
// already marked as playing in a room.
func (m *Matchmaker) AddToQueue(s *adler.Session) error {
	if s == nil {
		return ErrSessionNil
	}
	select {
	case m.commands <- matchmakingCommand{
		kind:    matchmakingCommandAdd,
		session: s,
	}:
		return nil
	default:
		return ErrMatchmakerBusy

	}

}

// RemoveFromQueue asks the matchmaker to remove a session from whichever queue
// currently owns it.
//
// The function is intentionally fire-and-forget because disconnect and leave
// flows usually care about quick cleanup, not an immediate confirmation. The
// worker removes the session from the main queue or the waiting queue, updates
// the session status to "left", and then reprocesses the remaining queue if a
// main-queue slot was freed.
func (m *Matchmaker) RemoveFromQueue(s *adler.Session) {
	if s == nil {
		return
	}
	m.commands <- matchmakingCommand{
		kind:    matchmakingCommandRemove,
		session: s,
	}

}

func (m *Matchmaker) handleAddToQueue(s *adler.Session, now time.Time) {
	queueStatus, ok := s.GetString(sessionKeyQueueStatus)
	if ok && queueStatus == queueStatusPlaying {
		return
	}

	if _, exists := m.inQueueMap[s]; exists {
		msg := &pb.QueueStatus{
			Payload: &pb.QueueStatus_QueueError{
				QueueError: &pb.QueueError{
					Message: "You are already in a queue",
				},
			},
		}
		s.Write(msg)
		return
	}

	if _, exists := m.inWaitQueueMap[s]; exists {
		msg := &pb.QueueStatus{
			Payload: &pb.QueueStatus_QueueError{
				QueueError: &pb.QueueError{
					Message: "You are already in a queue",
				},
			},
		}
		s.Write(msg)
		return
	}

	if m.maxQueue > 0 && m.queue.Len() >= m.maxQueue {
		m.addToWaitingQueue(s, now)
		m.processQueueTransitions(now)
	} else {
		m.addToMainQueue(s, now)
		m.processQueueTransitions(now)
	}
}

func (m *Matchmaker) addToMainQueue(s *adler.Session, now time.Time) {
	item := &queueItem{
		session:    s,
		enqueuedAt: now,
	}

	element := m.queue.PushBack(item)
	m.inQueueMap[s] = element
	delete(m.inWaitQueueMap, s)
	s.Set(sessionKeyQueueStatus, queueStatusQueued)
	msg := &pb.QueueStatus{
		Payload: &pb.QueueStatus_QueueJoined{
			QueueJoined: &pb.QueueJoined{
				Message: "You have joined the main queue",
			},
		},
	}
	_ = s.Write(msg)
}

func (m *Matchmaker) addToWaitingQueue(s *adler.Session, now time.Time) {
	item := &queueItem{
		session:    s,
		enqueuedAt: now,
	}

	element := m.waitQueue.PushBack(item)
	m.inWaitQueueMap[s] = element
	s.Set(sessionKeyQueueStatus, queueStatusWaiting)
	msg := &pb.QueueStatus{
		Payload: &pb.QueueStatus_WaitQueueJoined{
			WaitQueueJoined: &pb.WaitQueueJoined{
				Message: "The main queue is full. You have joined the waiting queue.",
			},
		},
	}
	_ = s.Write(msg)
}

func (m *Matchmaker) processQueueTransitions(now time.Time) {
	m.createFullRooms()
	m.promoteFromWaitingQueue(now)
	m.createFullRooms()
	m.createPartialRoomIfAllowed(now)
}

func (m *Matchmaker) createFullRooms() {
	for m.queue.Len() >= m.roomSize {
		m.createRoomFromQueue(m.roomSize)
	}
}

func (m *Matchmaker) createPartialRoomIfAllowed(now time.Time) {
	if m.queue.Len() < m.minRoomSize {
		return
	}

	if m.partialRoomTimeout > 0 {
		front := m.queue.Front()
		if front == nil {
			return
		}

		item := front.Value.(*queueItem)
		if now.Sub(item.enqueuedAt) < m.partialRoomTimeout { // must stay at least partialRoomTimeout time
			return
		}
	}

	size := m.queue.Len()
	size = min(size, m.roomSize)
	m.createRoomFromQueue(size)
}

func (m *Matchmaker) createRoomFromQueue(size int) {
	players := make([]*adler.Session, 0, size)
	for range size {
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
		id = uuid.New() // CAREFUL! Generating UUIDv4 instead of UUIDv7 could cause issues
	}

	roomID := "room_" + id.String()
	room := m.adler.NewRoom(roomID)

	for _, player := range players {
		room.Join(player)
		player.Set(sessionKeyQueueStatus, queueStatusPlaying)
		player.Set(sessionKeyRoomID, roomID)
	}

	msg := &pb.QueueStatus{
		Payload: &pb.QueueStatus_MatchFound{
			MatchFound: &pb.MatchFound{
				RoomId:  roomID,
				Players: int32(len(players)),
			},
		},
	}
	room.BroadcastAny(msg)
}

func (m *Matchmaker) promoteFromWaitingQueue(now time.Time) {
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

		msg := &pb.QueueStatus{
			Payload: &pb.QueueStatus_PromotedToQueue{
				PromotedToQueue: &pb.PromotedToQueue{
					Message: "A spot opened up. You are now in the main queue",
				},
			},
		}
		session.Write(msg)
	}
}

func (m *Matchmaker) handleRemoveFromQueue(s *adler.Session, now time.Time) {
	if element, exists := m.inQueueMap[s]; exists {
		m.queue.Remove(element)
		delete(m.inQueueMap, s)

		s.Set(sessionKeyQueueStatus, queueStatusLeft)
		m.processQueueTransitions(now)
		return
	}

	if element, exists := m.inWaitQueueMap[s]; exists {
		m.waitQueue.Remove(element)
		delete(m.inWaitQueueMap, s)
		s.Set(sessionKeyQueueStatus, queueStatusLeft)
		return
	}
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
