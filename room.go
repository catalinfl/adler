package adler

import "sync"

type Room struct {
	mu       sync.RWMutex
	name     string
	sessions map[*Session]struct{}
}

func newRoom(name string) *Room {
	return &Room{
		name:     name,
		sessions: make(map[*Session]struct{}),
		mu:       sync.RWMutex{},
	}
}

func (r *Room) addSession(s *Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[s] = struct{}{}
}

func (r *Room) removeSession(s *Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, s)
}

func (r *Room) broadcast(message message) {
	r.mu.RLock()
	sessions := make([]*Session, 0, len(r.sessions))
	for session := range r.sessions {
		sessions = append(sessions, session)
	}
	r.mu.RUnlock()

	for _, session := range sessions {
		session.writeMessage(message)
	}
}

func (r *Room) len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sessions)
}
