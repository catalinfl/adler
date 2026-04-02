package adler

import (
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gobwas/ws/wsutil"
)

type Session struct {
	Conn       net.Conn
	Request    *http.Request
	Protocol   string
	Keys       map[string]any
	output     chan message
	outputDone chan struct{}
	mu         sync.RWMutex
	closed     bool
	adler      *Adler
}

var (
	ErrWriteClosed = errors.New("Write session is closed")
	ErrBufferFull  = errors.New("Buffer is full")
)

func (s *Session) writeMessage(message message) {
	if s.isClosed() {
		s.adler.handlers.errorHandler(s, ErrWriteClosed)
		return
	}

	select {
	case s.output <- message:
	default:
		s.adler.handlers.errorHandler(s, ErrBufferFull)
	}
}

func (s *Session) isClosed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.closed
}

func (s *Session) close() {
	s.mu.Lock()
	closed := s.closed
	s.closed = true
	s.mu.Unlock()

	if !closed {
		s.Conn.Close()
		close(s.outputDone)
	}
}

func (s *Session) writePump() {
	ticker := time.NewTicker(s.adler.Config.PingPeriod)
	defer ticker.Stop()

loop:
	for {
		select {
		case message := <-s.output:
			err := wsutil.WriteClientMessage(s.Conn, 0x8, message.content)
			if err != nil {
				s.adler.handlers.errorHandler(s, err)
				break loop
			}
		case <-ticker.C:
			s.ping()
		case _, ok := <-s.outputDone:
			if !ok {
				break loop
			}
		}
	}

	s.close()
}

// write sends data from server to client
// through his connection
func (s *Session) write() {
	if s.isClosed() {

	}
}

func (s *Session) ping() {

}
