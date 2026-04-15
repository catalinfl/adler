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

// newCore allocates and initializes the core state and reusable buffers.
func newCore() *core {
	core := &core{
		sessions: make(map[*Session]struct{}),
		rooms:    nil,
		sessionPool: sync.Pool{
			New: func() any {
				s := make([]*Session, 0)
				return &s
			},
		},
	}
	core.closed.Store(false)
	return core
}

// isClosed reports whether the core is already closed.
func (h *core) isClosed() bool {
	return h.closed.Load()
}

// len returns the number of currently registered sessions.
func (h *core) len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.sessions)
}

// register adds a session to the active session set.
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

// unregister removes a session from the active session set.
func (h *core) unregister(s *Session) error {
	if s == nil {
		return ErrNilSession
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed.Load() {
		return ErrCoreClosed
	}

	delete(h.sessions, s)
	return nil
}

// exit sends a final message to all sessions and marks the core closed.
func (h *core) exit(message message) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed.Load() {
		return ErrCoreClosed
	}

	for session := range h.sessions {
		session.writeMessage(message)
	}

	h.sessions = nil
	h.closed.Store(true)
	return nil
}

// broadcast sends a message to every session matching the optional filter.
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

	clear(sessions)
	*sp = sessions[:0]
	h.sessionPool.Put(sp)
}
