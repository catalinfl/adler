package adler

import (
	"sync"
	"sync/atomic"
)

// core stores main server logic
type core struct {
	mu          sync.RWMutex
	sessions    map[*Session]struct{}
	rooms       map[string]*Room
	closed      atomic.Bool
	sessionPool sync.Pool
}

func newCore() *core {
	core := &core{
		sessions: make(map[*Session]struct{}),
		rooms:    nil,
		sessionPool: sync.Pool{
			New: func() any {
				return make([]*Session, 0)
			},
		},
	}
	core.closed.Store(false)
	return core
}

func (h *core) isClosed() bool {
	return h.closed.Load()
}

func (h *core) len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.sessions)
}

func (h *core) register(s *Session) error {
	if s == nil {
		return ErrNilSession
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.isClosed() {
		return ErrCoreClosed
	}

	if _, exists := h.sessions[s]; exists {
		return ErrAlreadyRegistered
	}

	h.sessions[s] = struct{}{}
	return nil
}

func (h *core) unregister(s *Session) error {
	if s == nil {
		return ErrNilSession
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.closed.Load() {
		return ErrCoreClosed
	}

	delete(h.sessions, s)
	return nil
}

func (h *core) exit(message message) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.closed.Load() {
		return ErrCoreClosed
	}

	for session := range h.sessions {
		session.writeMessage(message)
	}

	h.sessions = nil
	h.closed.Store(true)
	return nil
}

func (h *core) broadcast(message message) {
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
