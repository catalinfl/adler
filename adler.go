package adler

import (
	"encoding/json"
	"net"
	"net/http"
	"sync"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

type messageFunc func(*Session, []byte)
type errorFunc func(*Session, error)
type closeFunc func(*Session, int, string)
type sessionFunc func(*Session)
type filterFunc func(*Session) bool
type roomSessionFunc func(*Session, *Room)

type handlers struct {
	messageHandler           messageFunc
	messageHandlerBinary     messageFunc
	messageSentHandler       messageFunc
	messageSentHandlerBinary messageFunc
	errorHandler             errorFunc
	closeHandler             closeFunc
	connectHandler           sessionFunc
	disconnectHandler        sessionFunc
	pongHandler              sessionFunc
	onRoomLeave              roomSessionFunc
	onRoomJoin               roomSessionFunc
}

// Adler is the main websocket server orchestrator.
// It owns active sessions, rooms and all user-provided callbacks.
type Adler struct {
	// Config holds runtime behavior knobs used by session read/write loops.
	Config   *Config
	core     *core
	handlers handlers
	rooms    map[string]*Room
	roomsMu  sync.RWMutex
}

// New initializes a ready-to-use Adler instance.
// Options override default configuration values in construction order.
func New(options ...Option) *Adler {
	// no handler functions are initialized by default, all are nil
	handlers := handlers{}
	cfg := newConfig(options...)

	return &Adler{
		Config:   cfg,
		core:     newCore(),
		handlers: handlers,
		rooms:    make(map[string]*Room),
	}
}

// HandleRequest upgrades the HTTP request to websocket and serves the session.
// The call blocks until the websocket session exits.
func (a *Adler) HandleRequest(w http.ResponseWriter, r *http.Request) error {
	if a.core.isClosed() {
		return ErrCoreClosed
	}

	outputBufferSize := 1
	if a.Config != nil && a.Config.MessageBufferSize > 0 {
		outputBufferSize = int(a.Config.MessageBufferSize)
	}

	conn, err := a.upgradeWebSocket(r, w)
	if err != nil {
		return err
	}

	session := &Session{
		store:      make(map[string]any),
		request:    r,
		protocol:   r.Proto,
		conn:       conn,
		output:     make(chan message, outputBufferSize),
		outputDone: make(chan struct{}),
		mu:         sync.RWMutex{},
		closed:     false,
		adler:      a,
	}

	// must have a reader to reduce number of allocations per session
	session.reader = wsutil.NewReader(conn, ws.StateServerSide)

	if err := a.core.register(session); err != nil {
		_ = conn.Close()
		return err
	}

	if a.handlers.connectHandler != nil {
		a.handlers.connectHandler(session)
	}
	go session.writePump()
	session.readPump()

	session.mu.Lock()
	room := session.room
	session.room = nil
	session.mu.Unlock()

	if room != nil {
		room.mu.Lock()
		delete(room.sessions, session)
		room.mu.Unlock()

		if room.handlers.onLeave != nil {
			room.handlers.onLeave(session)
		}

		if a.handlers.onRoomLeave != nil {
			a.handlers.onRoomLeave(session, room)
		}
	}

	// connection must be unregistered forced after closing readPump
	a.core.unregister(session)

	session.close()
	if a.handlers.disconnectHandler != nil {
		a.handlers.disconnectHandler(session)
	}
	return nil
}

func (a *Adler) broadcast(msg []byte, messageType ws.OpCode, fn ...func(*Session) bool) error {
	if a.core.isClosed() {
		return ErrCoreClosed
	}

	var filter func(*Session) bool

	if len(fn) > 0 {
		filter = fn[0]
	}

	a.core.broadcast(message{
		messageType: messageType,
		content:     msg,
		filter:      filter,
	})

	return nil
}

// Broadcast sends a text websocket message to all connected sessions.
func (a *Adler) Broadcast(msg []byte) error {
	return a.broadcast(msg, ws.OpText)
}

// BroadcastFilter sends a text websocket message to sessions matching fn.
func (a *Adler) BroadcastFilter(msg []byte, fn func(*Session) bool) error {
	return a.broadcast(msg, ws.OpText, fn)
}

// BroadcastBinary sends a binary websocket message to all connected sessions.
func (a *Adler) BroadcastBinary(msg []byte) error {
	return a.broadcast(msg, ws.OpBinary)
}

// BroadcastBinaryFilter sends a binary websocket message to sessions matching fn.
func (a *Adler) BroadcastBinaryFilter(msg []byte, fn func(*Session) bool) error {
	return a.broadcast(msg, ws.OpBinary, fn)
}

// BroadcastJSON marshals v and broadcasts it as a text websocket message.
func (a *Adler) BroadcastJSON(v Map) error {
	content, err := json.Marshal(v)
	if err != nil {
		return err
	}

	return a.Broadcast(content)
}

// BroadcastJSONFilter marshals v and broadcasts it as text to sessions matching fn.
func (a *Adler) BroadcastJSONFilter(v Map, fn func(*Session) bool) error {
	content, err := json.Marshal(v)
	if err != nil {
		return err
	}

	return a.BroadcastFilter(content, fn)
}

// Len returns the current number of active sessions.
func (a *Adler) Len() int {
	return a.core.len()
}

// IsClosed reports whether the server core has been closed.
func (a *Adler) IsClosed() bool {
	return a.core.isClosed()
}

// Close queues a close frame for all sessions and marks the core as closed.
func (a *Adler) Close() error {
	if a.core.isClosed() {
		return ErrCoreClosed
	}

	err := a.core.exit(message{
		messageType: ws.OpClose,
		content:     []byte(""),
	})

	if err != nil {
		return ErrCoreExit
	}
	return nil
}

// NewRoom returns an existing room by name or creates a new one.
func (a *Adler) NewRoom(name string) *Room {
	a.roomsMu.Lock()
	defer a.roomsMu.Unlock()

	if a.rooms == nil {
		a.rooms = make(map[string]*Room)
	}

	if r, ok := a.rooms[name]; ok {
		return r
	}

	r := newRoom(name, a)
	a.rooms[name] = r
	return r
}

func (a *Adler) removeRoomIfEmpty(r *Room) {
	if r == nil || r.Len() != 0 {
		return
	}

	a.roomsMu.Lock()
	defer a.roomsMu.Unlock()

	if a.rooms == nil {
		return
	}

	current, ok := a.rooms[r.name]
	if !ok || current != r {
		return
	}

	if r.Len() == 0 {
		delete(a.rooms, r.name)
	}
}

// BroadcastOthers sends msg to all connected sessions except s.
func (a *Adler) BroadcastOthers(msg []byte, s *Session) error {
	return a.BroadcastFilter(msg, func(other *Session) bool {
		return other != s
	})
}

// SendTo sends msg only to the target session s.
func (a *Adler) SendTo(msg []byte, s *Session) error {
	return a.BroadcastFilter(msg, func(other *Session) bool {
		return other == s
	})
}

func (a *Adler) upgradeWebSocket(r *http.Request, w http.ResponseWriter) (net.Conn, error) {
	conn, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// OnRoomJoin registers a callback triggered after a session joins a room.
func (a *Adler) OnRoomJoin(fn func(*Session, *Room)) {
	a.handlers.onRoomJoin = fn
}

// OnRoomLeave registers a callback triggered after a session leaves a room.
func (a *Adler) OnRoomLeave(fn func(*Session, *Room)) {
	a.handlers.onRoomLeave = fn
}

// HandleConnect registers a callback triggered when a websocket session starts.
func (a *Adler) HandleConnect(fn func(*Session)) {
	a.handlers.connectHandler = fn
}

func (a *Adler) HandleClose(fn func(*Session, int, string)) {
	a.handlers.closeHandler = fn
}

// HandleDisconnect registers a callback triggered when a websocket session ends.
func (a *Adler) HandleDisconnect(fn func(*Session)) {
	a.handlers.disconnectHandler = fn
}

// HandlePong registers a callback for pong events.
func (a *Adler) HandlePong(fn func(*Session)) {
	a.handlers.pongHandler = fn
}

// HandleMessage registers the text-message handler.
func (a *Adler) HandleMessage(fn func(*Session, []byte)) {
	a.handlers.messageHandler = fn
}

// HandleMessageBinary registers the binary-message handler.
func (a *Adler) HandleMessageBinary(fn func(*Session, []byte)) {
	a.handlers.messageHandlerBinary = fn
}

// HandleSentMessage registers a callback for sent text messages.
func (a *Adler) HandleSentMessage(fn func(*Session, []byte)) {
	a.handlers.messageSentHandler = fn
}

// HandleSentMessageBinary registers a callback for sent binary messages.
func (a *Adler) HandleSentMessageBinary(fn func(*Session, []byte)) {
	a.handlers.messageSentHandlerBinary = fn
}

// HandleError registers the session-level error handler.
func (a *Adler) HandleError(fn func(*Session, error)) {
	a.handlers.errorHandler = fn
}
