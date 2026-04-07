package adler

import (
	"errors"
	"sync"
	"sync/atomic"
)

var (
	ErrHubClosed         = errors.New("hub is closed")
	ErrNilSession        = errors.New("nil session")
	ErrAlreadyRegistered = errors.New("session is already registered")
)

// hub stores main server logic
type hub struct {
	mu          sync.RWMutex
	sessions    map[*Session]struct{}
	rooms       map[string]*Room
	closed      atomic.Bool
	sessionPool sync.Pool
}

func newHub() *hub {
	hub := &hub{
		sessions: make(map[*Session]struct{}),
		rooms:    nil,
		sessionPool: sync.Pool{
			New: func() any {
				return make([]*Session, 0)
			},
		},
	}
	hub.closed.Store(false)
	return hub
}

func (h *hub) isClosed() bool {
	return h.closed.Load()
}

func (h *hub) len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.sessions)
}

func (h *hub) register(s *Session) error {
	if s == nil {
		return ErrNilSession
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.isClosed() {
		return ErrHubClosed
	}

	if _, exists := h.sessions[s]; exists {
		return ErrAlreadyRegistered
	}

	h.sessions[s] = struct{}{}
	return nil
}

func (h *hub) unregister(s *Session) error {
	if s == nil {
		return ErrNilSession
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.closed.Load() {
		return ErrHubClosed
	}

	delete(h.sessions, s)
	return nil
}

func (h *hub) exit(message message) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.closed.Load() {
		return ErrHubClosed
	}

	for session := range h.sessions {
		session.writeMessage(message)
	}

	h.sessions = nil
	h.closed.Store(true)
	return nil
}

func (h *hub) broadcast(message message) {
	h.mu.RLock()
	sp := h.sessionPool.Get().(*[]*Session)
	sessions := (*sp)[:0]

	for session := range h.sessions {
		sessions = append(sessions, session)
	}

	h.mu.RUnlock()

	for _, session := range sessions {
		if session == nil {
			continue
		}

		if message.filter == nil || message.filter(session) {
			session.writeMessage(message)
		}
	}

	clear(*sp)
	*sp = sessions[:0]
	h.sessionPool.Put(sp)
}
