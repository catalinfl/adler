package adler

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

// Session represents a single WebSocket client connection.
type Session struct {
	Conn       net.Conn
	Request    *http.Request
	Protocol   string
	Store      map[string]any
	output     chan message
	outputDone chan struct{}
	writeMu    sync.Mutex
	mu         sync.RWMutex
	identity   string
	closed     bool
	adler      *Adler
	reader     *wsutil.Reader
	readBuf    bytes.Buffer
}

// writeMessage enqueues an outbound message for asynchronous writing.
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

// writeFrame writes a websocket frame directly to the client connection.
func (s *Session) writeFrame(message message) error {
	if s.isClosed() {
		return ErrWriteClosed
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	writeWait := message.writeWait
	if writeWait <= 0 && s.adler != nil && s.adler.Config != nil {
		writeWait = s.adler.Config.WriteWait
	}

	if writeWait > 0 {
		if err := s.Conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
			return err
		}
		defer func() {
			_ = s.Conn.SetWriteDeadline(time.Time{})
		}()
	}

	err := wsutil.WriteServerMessage(s.Conn, message.messageType, message.content)
	if err != nil {
		return err
	}

	return nil
}

// writePump drains the output queue and periodically sends ping frames.
func (s *Session) writePump() {
	ticker := time.NewTicker(s.adler.Config.PingPeriod)
	defer ticker.Stop()

loop:
	for {
		select {
		case message := <-s.output:
			err := s.writeFrame(message)
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

// isClosed reports whether the session is already closed.
func (s *Session) isClosed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.closed
}

// close marks the session as closed and releases connection resources once.
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

// readPump continuously reads client frames and dispatches them to handlers.
func (s *Session) readPump() {
	dispatchAsync := s.adler.Config.DispatchAsync

	for {
		header, err := s.reader.NextFrame()
		if err != nil {
			s.adler.handlers.errorHandler(s, err)
			return
		}

		s.readBuf.Reset()
		if _, err := s.readBuf.ReadFrom(s.reader); err != nil {
			s.adler.handlers.errorHandler(s, err)
			return
		}

		payload := s.readBuf.Bytes()

		if dispatchAsync {
			cp := make([]byte, len(payload))
			copy(cp, payload)
			go s.handleMessage(header.OpCode, cp)
		} else {
			s.handleMessage(header.OpCode, payload)
		}
	}
}

// handleMessage routes text and binary frames to the configured handlers.
func (s *Session) handleMessage(op ws.OpCode, message []byte) {
	switch op {
	case ws.OpText:
		s.adler.handlers.messageHandler(s, message)
	case ws.OpBinary:
		s.adler.handlers.messageHandlerBinary(s, message)
	}
}

// ping sends a websocket ping control frame.
func (s *Session) ping() {
	wsutil.WriteServerMessage(s.Conn, ws.OpPing, nil)
}

// WriteText queues a text message to be sent to the client.
func (s *Session) WriteText(message []byte) error {
	return s.write(message, ws.OpText)
}

// write adds the message in queue through writeMessage.
func (s *Session) write(msg []byte, code ws.OpCode, deadline ...time.Duration) error {
	if s.isClosed() {
		return ErrSessionClosed
	}

	m := message{
		messageType: code,
		content:     msg,
	}
	if len(deadline) > 0 {
		m.writeWait = deadline[0]
	}

	s.writeMessage(m)
	return nil
}

// WriteTextWithDeadline queues a text message with a custom write deadline.
func (s *Session) WriteTextWithDeadline(message []byte, deadline time.Duration) error {
	return s.write(message, ws.OpText, deadline)
}

// WriteJSON marshals v to JSON and queues it as a text message.
func (s *Session) WriteJSON(v any) error {
	jsonContent, err := json.Marshal(v)
	if err != nil {
		return err
	}

	return s.write(jsonContent, ws.OpText)
}

// WriteJSONWithDeadline queues a JSON payload with a custom write deadline.
func (s *Session) WriteJSONWithDeadline(message any, deadline time.Duration) error {
	jsonContent, err := json.Marshal(message)
	if err != nil {
		return err
	}

	return s.write(jsonContent, ws.OpText, deadline)
}

// WriteBinary queues a binary message to be sent to the client.
func (s *Session) WriteBinary(msg []byte) error {
	return s.write(msg, ws.OpBinary)
}

// WriteBinaryWithDeadline queues a binary message with a custom write deadline.
func (s *Session) WriteBinaryWithDeadline(msg []byte, deadline time.Duration) error {
	return s.write(msg, ws.OpBinary, deadline)
}

// Close queues a websocket close frame, optionally with a close payload.
func (s *Session) Close(msg ...[]byte) error {
	if s.isClosed() {
		return ErrSessionClosed
	}

	message := message{
		messageType: ws.OpClose,
	}

	if len(msg) > 0 {
		message.content = msg[0]
	}

	s.writeMessage(message)
	return nil
}

// Set ensures session key storage is initialized.
func (s *Session) Set(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Store[key] = value
}

func (s *Session) Get(key string) (any, error) {
	s.mu.RLock()
	val, exists := s.Store[key]
	s.mu.RUnlock()
	if !exists {
		return nil, ErrKeyNotFound
	}

	return val, nil
}

func (s *Session) Unset(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Store, key)
}

func (s *Session) Has(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, exists := s.Store[key]
	return exists
}

func (s *Session) SetNX(key string, value any) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, exists := s.Store[key]
	if exists {
		return false
	}

	s.Store[key] = value
	return true
}

func (s *Session) Keys() []string {
	keys := make([]string, len(s.Store))
	i := 0

	s.mu.RLock()
	for k := range s.Store {
		keys[i] = k
		i++
	}
	s.mu.RUnlock()

	return keys
}

func (s *Session) Values() []any {
	values := make([]any, len(s.Store))
	i := 0

	s.mu.RLock()
	for _, v := range s.Store {
		values[i] = v
		i++
	}
	s.mu.RUnlock()

	return values
}

func (s *Session) GetString(key string) (string, bool) {
	s.mu.RLock()
	val, exists := s.Store[key]
	s.mu.RUnlock()

	if !exists {
		return "", false
	}

	if v, ok := val.(string); ok {
		return v, true
	}

	return "", false
}

func (s *Session) GetInt(key string) (int, bool) {
	s.mu.RLock()
	val, exists := s.Store[key]
	s.mu.RUnlock()

	if !exists {
		return 0, false
	}

	if v, ok := val.(int); ok {
		return v, true
	}

	return 0, false
}

func (s *Session) GetInt64(key string) (int64, bool) {
	s.mu.RLock()
	val, exists := s.Store[key]
	s.mu.RUnlock()

	if !exists {
		return 0, false
	}
	if v, ok := val.(int64); ok {
		return v, true
	}
	return 0, false
}

func (s *Session) GetFloat(key string) (float64, bool) {
	s.mu.RLock()
	val, exists := s.Store[key]
	s.mu.RUnlock()

	if !exists {
		return 0, false
	}
	if v, ok := val.(float64); ok {
		return v, true
	}
	return 0, false
}

func (s *Session) GetBool(key string) (bool, bool) {
	s.mu.RLock()
	val, exists := s.Store[key]
	s.mu.RUnlock()

	if !exists {
		return false, false
	}
	if v, ok := val.(bool); ok {
		return v, true
	}
	return false, false
}

func (s *Session) Incr(key string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ptr, exists := s.Store[key]
	if !exists {
		return 0, ErrKeyNotFound
	}

	p, ok := ptr.(*int64)
	if !ok {
		return 0, ErrTypeAssertionFailed
	}
	*p++
	return *p, nil
}

func (s *Session) Decr(key string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ptr, exists := s.Store[key]
	if !exists {
		return 0, ErrKeyNotFound
	}

	p, ok := ptr.(*int64)
	if !ok {
		return 0, ErrTypeAssertionFailed
	}

	*p--
	return *p, nil
}

func (s *Session) SetIdentity(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.identity = id
}

func (s *Session) Identity() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.identity != ""
}

func (s *Session) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.Store)
}

func (s *Session) LocalAddr() net.Addr {
	return s.Conn.LocalAddr()
}

func (s *Session) RemoteAddr() net.Addr {
	return s.Conn.RemoteAddr()
}
