package adler

import (
	"encoding/json"
	"sync"
	"sync/atomic"

	"github.com/gobwas/ws"
)

type Room struct {
	mu       sync.RWMutex
	adler    *Adler
	handlers roomHandlers
	name     string
	sessions map[*Session]struct{}
	closed   atomic.Bool
}

type roomHandlers struct {
	onJoin  func(*Session)
	onLeave func(*Session)
}

func newRoom(name string, a *Adler) *Room {
	room := &Room{
		name:     name,
		sessions: make(map[*Session]struct{}),
		mu:       sync.RWMutex{},
		adler:    a,
	}
	room.closed.Store(false)
	return room
}

func (r *Room) isClosed() bool {
	return r.closed.Load()
}

func (r *Room) Name() string {
	return r.name
}

func (r *Room) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sessions)
}

func (r *Room) Sessions() []*Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Session, 0, len(r.sessions))
	for k := range r.sessions {
		out = append(out, k)
	}
	return out
}

func (r *Room) OpenRoom() {
	r.closed.Store(false)
}

func (r *Room) CloseRoom() {
	r.closed.Store(true)
}

func (r *Room) Join(s *Session) {
	if r.isClosed() || s == nil {
		return
	}

	s.mu.Lock()
	// keep old room to clear the session from it
	prev := s.room
	// session will have s room
	r.mu.Lock()
	s.room = r

	if r.sessions == nil {
		r.sessions = make(map[*Session]struct{})
	}

	r.sessions[s] = struct{}{}
	r.mu.Unlock()
	s.mu.Unlock()

	if prev != nil && prev != r {
		prev.mu.Lock()
		delete(prev.sessions, s)
		prevEmpty := len(prev.sessions) == 0
		if prevEmpty {
			prev.sessions = nil
			prev.closed.Store(true)
		}
		prev.mu.Unlock()

		if prev.handlers.onLeave != nil {
			prev.handlers.onLeave(s)
		}

		if prev.adler.handlers.onRoomLeave != nil {
			prev.adler.handlers.onRoomLeave(s, prev)
		}

		// remove previous room if it doesn't have sessions
		// to-do config flag
		if prevEmpty {
			prev.adler.removeRoomIfEmpty(prev)
		}
	}

	if r.handlers.onJoin != nil {
		r.handlers.onJoin(s)
	}

	if r.adler.handlers.onRoomJoin != nil {
		r.adler.handlers.onRoomJoin(s, r)
	}
}

// use leave to let session leave the current room
// it sets s.room to nil for current room and deletes session from room
func (r *Room) Leave(s *Session) {
	if s == nil {
		return
	}

	s.mu.Lock()
	if s.room != r {
		s.mu.Unlock()
		return
	}
	s.room = nil
	r.mu.Lock()
	delete(r.sessions, s)
	empty := len(r.sessions) == 0
	if empty {
		r.sessions = nil
		r.closed.Store(true)
	}
	r.mu.Unlock() // room mutex
	s.mu.Unlock() // session mutex

	if r.handlers.onLeave != nil {
		r.handlers.onLeave(s)
	}

	if r.adler.handlers.onRoomLeave != nil {
		r.adler.handlers.onRoomLeave(s, r)
	}

	if empty {
		r.adler.removeRoomIfEmpty(r)
	}
}

func (r *Room) HandleJoin(fn func(*Session)) {
	r.handlers.onJoin = fn
}

func (r *Room) HandleLeave(fn func(*Session)) {
	r.handlers.onLeave = fn
}

func (r *Room) Broadcast(msg []byte) {
	r.broadcast(msg, ws.OpText, nil)
}

func (r *Room) BroadcastBinary(msg []byte) {
	r.broadcast(msg, ws.OpBinary, nil)
}

func (r *Room) BroadcastFilter(msg []byte, filter func(*Session) bool) {
	r.broadcast(msg, ws.OpText, filter)
}

func (r *Room) BroadcastJSON(v any) error {
	return r.BroadcastJSONFilter(v, nil)
}

func (r *Room) BroadcastJSONFilter(v any, filter func(*Session) bool) error {
	content, err := json.Marshal(v)
	if err != nil {
		return err
	}

	r.broadcast(content, ws.OpText, filter)
	return nil
}

func (r *Room) broadcast(content []byte, opCode ws.OpCode, filter func(*Session) bool) {
	r.mu.RLock()
	sessions := make([]*Session, 0, len(r.sessions))
	for session := range r.sessions {
		sessions = append(sessions, session)
	}
	r.mu.RUnlock()

	message := message{
		messageType: opCode,
		content:     content,
	}

	for _, session := range sessions {
		if filter == nil || filter(session) {
			session.writeMessage(message)
		}
	}
}
